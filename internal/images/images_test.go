package images

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// encodePNG renders an image to PNG bytes, which is the shape Process takes.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	return buf.Bytes()
}

// solid builds an image of one colour, which is line art by any measure.
func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// noisy builds an image with far more than 256 distinct colours.
func noisy(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8((x*7 + y*3) % 256),
				G: uint8((x*13 + y*11) % 256),
				B: uint8((x*29 + y*17) % 256),
				A: 255,
			})
		}
	}
	return img
}

func TestLineArtBecomesPalettedPNG(t *testing.T) {
	src := Source{Encoded: encodePNG(t, solid(40, 30, color.NRGBA{200, 30, 30, 255})), Format: "png"}

	got, err := Process(DefaultConfig(), src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png for line art", got.MediaType)
	}

	img, err := png.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatalf("decoding output: %v", err)
	}
	if _, ok := img.(*image.Paletted); !ok {
		t.Errorf("output is %T, want *image.Paletted; palettizing is why PNG is "+
			"chosen for line art", img)
	}
}

func TestPhotographicBecomesJPEG(t *testing.T) {
	src := Source{Encoded: encodePNG(t, noisy(64, 64)), Format: "png"}

	got, err := Process(DefaultConfig(), src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q, want image/jpeg for many-colour content", got.MediaType)
	}
	if _, err := jpeg.Decode(bytes.NewReader(got.Data)); err != nil {
		t.Errorf("output is not decodable JPEG: %v", err)
	}
}

func TestScalingPreservesAspectRatio(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxWidth = 50

	got, err := Process(cfg, Source{Encoded: encodePNG(t, noisy(200, 100)), Format: "png"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.Width != 50 {
		t.Errorf("Width = %d, want 50", got.Width)
	}
	if got.Height != 25 {
		t.Errorf("Height = %d, want 25 for a 2:1 source", got.Height)
	}
}

func TestNoUpscaling(t *testing.T) {
	// A small image must not be enlarged to the limit.
	cfg := DefaultConfig()
	cfg.MaxWidth = 1000

	got, err := Process(cfg, Source{Encoded: encodePNG(t, noisy(40, 30)), Format: "png"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.Width != 40 || got.Height != 30 {
		t.Errorf("output is %dx%d, want the original 40x30", got.Width, got.Height)
	}
}

func TestDCTPassthrough(t *testing.T) {
	// Spec 4.7 keeps a DCT stream unmodified when no scaling applies.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, noisy(64, 64), &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	original := buf.Bytes()

	cfg := DefaultConfig()
	cfg.MaxWidth = 0 // no scaling

	got, err := Process(cfg, Source{Encoded: original, Format: "jpg", DCTPassthrough: true})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !got.Passthrough {
		t.Error("Passthrough is false; the bytes should have been kept")
	}
	if !bytes.Equal(got.Data, original) {
		t.Error("passthrough re-encoded the image")
	}
}

func TestScalingDefeatsPassthrough(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, noisy(200, 200), &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.MaxWidth = 50

	got, err := Process(cfg, Source{Encoded: buf.Bytes(), Format: "jpg", DCTPassthrough: true})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.Passthrough {
		t.Error("Passthrough survived a scale; the pixels had to be resampled")
	}
	if got.Width != 50 {
		t.Errorf("Width = %d, want 50", got.Width)
	}
}

func TestGrayscaleDefeatsPassthrough(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, noisy(64, 64), &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.MaxWidth = 0
	cfg.Mode = ModeGrayscale

	got, err := Process(cfg, Source{Encoded: buf.Bytes(), Format: "jpg", DCTPassthrough: true})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.Passthrough {
		t.Error("Passthrough survived grayscale conversion")
	}
}

func TestGrayscaleMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeGrayscale

	got, err := Process(cfg, Source{Encoded: encodePNG(t, noisy(40, 40)), Format: "png"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatalf("decoding output: %v", err)
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r != g || g != bl {
				t.Fatalf("pixel at (%d,%d) is not gray: %d %d %d", x, y, r, g, bl)
			}
		}
	}
}

func TestDitherQuantizesToLevels(t *testing.T) {
	// Spec 5.1 quantizes to 16 gray levels for the crosspoint profile.
	cfg := DefaultConfig()
	cfg.Dither = true
	cfg.GrayLevels = 16
	cfg.ForceJPEG = true

	got, err := Process(cfg, Source{Encoded: encodePNG(t, noisy(64, 64)), Format: "png"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q; ForceJPEG must suppress PNG", got.MediaType)
	}
}

func TestFloydSteinbergLevels(t *testing.T) {
	// The quantizer itself, before JPEG blurs the result.
	src := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			src.SetGray(x, y, color.Gray{Y: uint8(x * 4)})
		}
	}

	out := floydSteinberg(src, 16)
	seen := map[uint8]bool{}
	b := out.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			seen[out.GrayAt(x, y).Y] = true
		}
	}
	if len(seen) > 16 {
		t.Errorf("quantizer produced %d distinct levels, want at most 16", len(seen))
	}
	if len(seen) < 2 {
		t.Errorf("quantizer collapsed the gradient to %d level(s)", len(seen))
	}
}

func TestDigestIsStableAndDistinct(t *testing.T) {
	a := encodePNG(t, noisy(32, 32))
	b := encodePNG(t, solid(32, 32, color.NRGBA{1, 2, 3, 255}))

	ra, err := Process(DefaultConfig(), Source{Encoded: a, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	ra2, err := Process(DefaultConfig(), Source{Encoded: a, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	rb, err := Process(DefaultConfig(), Source{Encoded: b, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}

	if ra.Digest != ra2.Digest {
		t.Error("the same pixels produced different digests")
	}
	if ra.Digest == rb.Digest {
		t.Error("different pixels produced the same digest")
	}
}

func TestDigestIgnoresSourceEncoding(t *testing.T) {
	// Deduplication keys on decoded pixels, so the same picture stored as PNG
	// and as a lossless re-encode must merge.
	img := solid(32, 32, color.NRGBA{10, 20, 30, 255})

	var a bytes.Buffer
	if err := png.Encode(&a, img); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Skip("the two encodings came out identical")
	}

	ra, err := Process(DefaultConfig(), Source{Encoded: a.Bytes(), Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	rb, err := Process(DefaultConfig(), Source{Encoded: b.Bytes(), Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if ra.Digest != rb.Digest {
		t.Error("two encodings of one picture produced different digests")
	}
}

func TestAlphaFlattenedOntoWhite(t *testing.T) {
	// Spec 4.7 composites alpha onto white. A reader shows pages on white, so
	// a transparent region left black would invert the artwork.
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			// Fully transparent black.
			img.Set(x, y, color.NRGBA{0, 0, 0, 0})
		}
	}

	cfg := DefaultConfig()
	cfg.Mode = ModeGrayscale

	got, err := Process(cfg, Source{Encoded: encodePNG(t, img), Format: "png"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	out, _, err := image.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := out.At(5, 5).RGBA(); r < 0xF000 {
		t.Errorf("transparent black flattened to %d, want near white", r>>8)
	}
}

func TestDropModeRefuses(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeDrop
	if _, err := Process(cfg, Source{Encoded: encodePNG(t, noisy(8, 8)), Format: "png"}); err == nil {
		t.Error("Process accepted an image in drop mode")
	}
}

func TestUndecodableInputIsAnError(t *testing.T) {
	if _, err := Process(DefaultConfig(), Source{
		Encoded: []byte("not an image at all"), Format: "png",
	}); err == nil {
		t.Error("Process accepted garbage")
	}
}

func TestCollectPaletteEarlyExit(t *testing.T) {
	// A many-colour image must give up rather than build a huge map.
	if palette, ok := collectPalette(noisy(128, 128), 256); ok || palette != nil {
		t.Errorf("collectPalette returned %d entries for a noisy image, want a bailout",
			len(palette))
	}
	// A few-colour image must return every colour it found.
	palette, ok := collectPalette(solid(16, 16, color.NRGBA{7, 8, 9, 255}), 256)
	if !ok || len(palette) != 1 {
		t.Errorf("collectPalette on a solid image returned %d entries (ok=%v), want 1",
			len(palette), ok)
	}
}

func TestProcessIsDeterministic(t *testing.T) {
	src := Source{Encoded: encodePNG(t, noisy(48, 48)), Format: "png"}
	a, err := Process(DefaultConfig(), src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Process(DefaultConfig(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Data, b.Data) {
		t.Error("two runs produced different bytes")
	}
}

func TestLineArtClassifiedBeforeReduction(t *testing.T) {
	// The classification must run on the source image. Each reduction
	// destroys the evidence in a different direction, so deciding afterwards
	// gets it wrong in a way that depends on which reductions are enabled.

	// Grayscale leaves at most 256 values, so a photograph would look like
	// line art if classified after it.
	cfg := DefaultConfig()
	cfg.Mode = ModeGrayscale
	got, err := Process(cfg, Source{Encoded: encodePNG(t, noisy(64, 64)), Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "image/jpeg" {
		t.Errorf("a grayscaled photograph became %s; classification ran after "+
			"the colour reduction", got.MediaType)
	}

	// Smooth resampling interpolates new colours, so a chart would look like
	// a photograph if classified after it.
	cfg = DefaultConfig()
	cfg.MaxWidth = 32
	got, err = Process(cfg, Source{Encoded: encodePNG(t, chart(200, 200)), Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "image/png" {
		t.Errorf("a scaled chart became %s; classification ran after the "+
			"resample", got.MediaType)
	}

	// Dithering scatters flat regions into noise, with the same effect.
	cfg = DefaultConfig()
	cfg.Dither = true
	cfg.GrayLevels = 16
	got, err = Process(cfg, Source{Encoded: encodePNG(t, chart(64, 64)), Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "image/png" {
		t.Errorf("a dithered chart became %s; classification ran after the "+
			"dither", got.MediaType)
	}
}

func TestLineArtScalesWithoutInterpolation(t *testing.T) {
	// Nearest-neighbour keeps the palette exact. Catmull-Rom would blend
	// across every edge and leave hundreds of intermediate colours, which
	// both blurs the artwork and defeats the paletted PNG.
	cfg := DefaultConfig()
	cfg.MaxWidth = 64

	got, err := Process(cfg, Source{Encoded: encodePNG(t, chart(256, 256)), Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", got.MediaType)
	}

	img, err := png.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := img.(*image.Paletted)
	if !ok {
		t.Fatalf("output is %T, want *image.Paletted", img)
	}
	// The source has four colours; interpolation would multiply that.
	if len(p.Palette) > 8 {
		t.Errorf("palette grew to %d entries; the resample interpolated",
			len(p.Palette))
	}
}

// chart builds a flat four-colour image, the shape of a diagram or plot.
func chart(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	palette := []color.NRGBA{
		{255, 255, 255, 255}, {0, 0, 0, 255},
		{200, 30, 30, 255}, {30, 30, 200, 255},
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, palette[((x/16)+(y/16))%len(palette)])
		}
	}
	return img
}
