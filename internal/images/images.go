// Package images re-encodes extracted PDF images for EPUB delivery. It is
// the second half of stage 7 in spec section 4.7.
package images

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"

	// hhrutter/tiff rather than x/image/tiff: pdfcpu renders CMYK images to
	// TIFF, and the upstream decoder rejects that colour model. This is the
	// same BSD-3 fork pdfcpu itself uses for the encode side.
	_ "github.com/hhrutter/tiff"
	xdraw "golang.org/x/image/draw"
)

// Mode selects how images are handled, mirroring the --images flag.
type Mode string

const (
	// ModeKeep retains images in their original colour.
	ModeKeep Mode = "keep"
	// ModeGrayscale converts images to grayscale.
	ModeGrayscale Mode = "grayscale"
	// ModeDrop discards images entirely.
	ModeDrop Mode = "drop"
)

// Config carries the tunable parts of image processing. The root package owns
// the public option set and converts into this at the boundary.
type Config struct {
	Mode Mode

	// MaxWidth is the longest edge in pixels; 0 disables scaling.
	MaxWidth int

	// JPEGQuality is the quality for photographic content. Spec 4.7: 85.
	JPEGQuality int

	// UniqueColorThreshold is the unique-colour count at or below which an
	// image is treated as line art and encoded as PNG. Spec 4.7: 256.
	UniqueColorThreshold int

	// Dither quantizes to GrayLevels and applies Floyd-Steinberg error
	// diffusion before encoding. Spec 5.1 turns this on for the crosspoint
	// profile: dithering before JPEG limits the ringing that a low-bit-depth
	// E Ink panel would otherwise render as visible banding.
	Dither bool
	// GrayLevels is the quantization depth when Dither is set. Spec 5.1: 16.
	GrayLevels int

	// ForceJPEG suppresses PNG output entirely.
	//
	// Spec 5.1 originally set this for crosspoint on the strength of the
	// stock firmware documenting only JPG and BMP. Reading the CrossPoint
	// firmware settled it the other way (spec section 13, closed
	// 2026-08-01): its EPUB image path decodes PNG, including the indexed
	// form, and has no BMP decoder at all. Nothing sets this now; it remains
	// for a reader that genuinely cannot take PNG.
	ForceJPEG bool
	// DitherQuality is the JPEG quality when Dither is set. Spec 5.1: 90.
	DitherQuality int
}

// DefaultConfig returns the documented defaults from spec section 4.7.
func DefaultConfig() Config {
	return Config{
		Mode:                 ModeKeep,
		MaxWidth:             1600,
		JPEGQuality:          85,
		UniqueColorThreshold: 256,
		GrayLevels:           16,
		DitherQuality:        90,
	}
}

// Encoded is a processed image ready to place in the EPUB container.
type Encoded struct {
	// Data is the encoded bytes.
	Data []byte
	// MediaType is the manifest media type, e.g. "image/jpeg".
	MediaType string
	// Ext is the filename extension without a dot.
	Ext string
	// Width and Height are the final pixel dimensions.
	Width, Height int
	// Digest is the SHA-256 of the decoded pixels, which is what images are
	// deduplicated by. Two encodings of the same picture share a digest.
	Digest string
	// Passthrough reports that the original DCT bytes were kept unmodified.
	Passthrough bool
}

// Source is one image to process.
type Source struct {
	// Encoded is the image as it came out of the PDF.
	Encoded []byte
	// Format is "jpg", "png", or "tif".
	Format string
	// DCTPassthrough marks a JPEG that arrived unmodified from the PDF.
	DCTPassthrough bool
}

// Process decodes, scales, and re-encodes one image.
//
// Spec section 4.7 keeps a DCT stream unmodified when no scaling applies,
// which both preserves quality and skips a decode-encode round trip. That
// shortcut is only safe when nothing else in the pipeline needs the pixels,
// so grayscale conversion, dithering, and scaling all disable it.
func Process(cfg Config, src Source) (*Encoded, error) {
	if cfg.Mode == ModeDrop {
		return nil, fmt.Errorf("images: mode is drop")
	}

	img, _, err := image.Decode(bytes.NewReader(src.Encoded))
	if err != nil {
		return nil, fmt.Errorf("images: decoding %s: %w", src.Format, err)
	}

	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("images: decoded to an empty image")
	}

	digest := pixelDigest(img)
	needsScale := cfg.MaxWidth > 0 && maxEdge(b) > cfg.MaxWidth
	needsGray := cfg.Mode == ModeGrayscale || cfg.Dither

	if src.DCTPassthrough && !needsScale && !needsGray {
		return &Encoded{
			Data:        src.Encoded,
			MediaType:   "image/jpeg",
			Ext:         "jpg",
			Width:       b.Dx(),
			Height:      b.Dy(),
			Digest:      digest,
			Passthrough: true,
		}, nil
	}

	// Whether this is line art is decided on the source image, before any
	// reduction, and it drives everything that follows.
	//
	// It cannot be decided later. Every reduction destroys the evidence, each
	// in a different direction. Grayscale conversion leaves at most 256
	// distinct values by construction, so afterwards every grayscale image
	// looks like line art and a photograph would ship as an enormous PNG.
	// Smooth resampling interpolates new intermediate colours, so afterwards a
	// 255-colour chart looks like a photograph. Dithering scatters flat
	// regions into noise, with the same effect.
	lineArt := false
	if !cfg.ForceJPEG {
		_, lineArt = collectPalette(img, cfg.UniqueColorThreshold)
	}

	if needsScale {
		img = scale(img, cfg.MaxWidth, lineArt)
		b = img.Bounds()
	}
	if needsGray {
		img = toGray(img)
	}

	var palette []color.Color
	if lineArt {
		// Re-collected because grayscale conversion changed the values; the
		// count can only have fallen, so this still succeeds.
		palette, _ = collectPalette(img, cfg.UniqueColorThreshold)
	} else if cfg.Dither {
		if gray, ok := img.(*image.Gray); ok {
			img = floydSteinberg(gray, cfg.GrayLevels)
		}
	}

	enc, err := encode(cfg, img, palette)
	if err != nil {
		return nil, err
	}
	enc.Width, enc.Height = b.Dx(), b.Dy()
	enc.Digest = digest
	return enc, nil
}

func maxEdge(b image.Rectangle) int {
	if b.Dx() > b.Dy() {
		return b.Dx()
	}
	return b.Dy()
}

// pixelDigest hashes the decoded pixels, so the same picture stored twice in
// a PDF deduplicates to one manifest entry regardless of its encodings.
func pixelDigest(img image.Image) string {
	h := sha256.New()
	b := img.Bounds()
	var hdr [16]byte
	putInt(hdr[0:8], b.Dx())
	putInt(hdr[8:16], b.Dy())
	h.Write(hdr[:])

	// NRGBA gives a stable byte layout across source image types, so two
	// encodings of one picture hash alike. Converting a row at a time rather
	// than the whole image keeps the scratch buffer proportional to the width
	// instead of the area: a 4000 by 3000 source needed 48 MB of temporary
	// NRGBA against spec section 9's budget, and now needs 16 KB. The bytes
	// fed to the hash are identical either way.
	row := image.NewNRGBA(image.Rect(0, 0, b.Dx(), 1))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		draw.Draw(row, row.Bounds(), img, image.Point{X: b.Min.X, Y: y}, draw.Src)
		h.Write(row.Pix)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func putInt(b []byte, v int) {
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
}

// scale resamples so the longest edge is at most maxEdge.
//
// Spec section 4.7 specifies Catmull-Rom, which is right for photographs.
// Line art takes nearest-neighbour instead: Catmull-Rom interpolates new
// colours across every edge, which blurs the artwork and destroys the palette
// that makes PNG the right container for it. On a low-bit-depth E Ink panel
// those interpolated edges are what banding is made of.
func scale(img image.Image, maxEdgePx int, lineArt bool) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return img
	}

	var nw, nh int
	if w >= h {
		nw = maxEdgePx
		nh = int(math.Round(float64(h) * float64(maxEdgePx) / float64(w)))
	} else {
		nh = maxEdgePx
		nw = int(math.Round(float64(w) * float64(maxEdgePx) / float64(h)))
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	// The destination is RGBA rather than NRGBA for speed, and it is also the
	// more correct of the two.
	//
	// x/image/draw generates a fast path per concrete destination type and
	// falls back to a generic RGBA64Image path otherwise. NRGBA has no
	// generated path, so the vertical pass ran through the fallback: on the
	// corpus's largest photograph that made scaling 13% of the whole
	// conversion, and the profile showed scaleX taking the RGBA fast path
	// while scaleY did not.
	//
	// Resampling premultiplied values is also what compositing requires.
	// Averaging non-premultiplied colour across a partly transparent edge
	// weights fully transparent pixels as though they were opaque, which
	// fringes the edge with whatever colour happens to sit under the alpha.
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	filter := xdraw.Interpolator(xdraw.CatmullRom)
	if lineArt {
		filter = xdraw.NearestNeighbor
	}
	filter.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// toGray converts to 8-bit grayscale, compositing any alpha onto white.
//
// Spec section 4.7 composites /SMask alpha onto white; an EPUB reader shows
// pages on white, so a transparent region left black would invert the artwork.
func toGray(img image.Image) *image.Gray {
	b := img.Bounds()
	dst := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))

	white := image.NewUniform(color.White)
	flat := image.NewNRGBA(dst.Bounds())
	draw.Draw(flat, flat.Bounds(), white, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, b.Min, draw.Over)

	draw.Draw(dst, dst.Bounds(), flat, image.Point{}, draw.Src)
	return dst
}

// flattenOntoWhite composites alpha onto a white background without changing
// the colour model.
func flattenOntoWhite(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// floydSteinberg quantizes to n gray levels with error diffusion.
//
// Spec section 5.1 applies this before JPEG encoding for the crosspoint
// profile: quantizing first limits the ringing artifacts a low-bit-depth
// panel would otherwise show as banding.
func floydSteinberg(src *image.Gray, levels int) *image.Gray {
	if levels < 2 {
		levels = 2
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	// Work in float so diffused error does not clip prematurely.
	buf := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf[y*w+x] = float64(src.GrayAt(b.Min.X+x, b.Min.Y+y).Y)
		}
	}

	step := 255.0 / float64(levels-1)
	dst := image.NewGray(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			old := buf[y*w+x]
			q := math.Round(old/step) * step
			if q < 0 {
				q = 0
			}
			if q > 255 {
				q = 255
			}
			dst.SetGray(x, y, color.Gray{Y: uint8(q)})

			err := old - q
			// Standard Floyd-Steinberg weights: 7/16 right, 3/16 down-left,
			// 5/16 down, 1/16 down-right.
			diffuse := func(dx, dy int, weight float64) {
				nx, ny := x+dx, y+dy
				if nx < 0 || nx >= w || ny < 0 || ny >= h {
					return
				}
				buf[ny*w+nx] += err * weight
			}
			diffuse(1, 0, 7.0/16)
			diffuse(-1, 1, 3.0/16)
			diffuse(0, 1, 5.0/16)
			diffuse(1, 1, 1.0/16)
		}
	}
	return dst
}

// encode picks a container and writes the image.
//
// Spec section 4.7 decides between JPEG and PNG on unique-colour count: line
// art compresses far better and stays sharp as PNG, while a photograph would
// balloon. The crosspoint profile forces JPEG regardless, because the stock
// firmware documents only JPG and BMP.
func encode(cfg Config, img image.Image, palette []color.Color) (*Encoded, error) {
	quality := cfg.JPEGQuality
	if cfg.Dither && cfg.DitherQuality > 0 {
		quality = cfg.DitherQuality
	}
	if quality <= 0 || quality > 100 {
		quality = 85
	}

	var buf bytes.Buffer
	if palette != nil {
		// Line art. Palettizing is the whole reason PNG wins here: Go's
		// encoder otherwise writes full RGBA, which turned a 255-colour test
		// chart into 1.7 MB where the paletted form is a fraction of that.
		// DefaultCompression rather than BestCompression. Across the corpus
		// the strongest level buys 0.05% of output size for 4% of conversion
		// time, which is the wrong trade for a format already carrying most
		// of its win in the palette.
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, palettize(img, palette)); err != nil {
			return nil, fmt.Errorf("images: encoding PNG: %w", err)
		}
		return &Encoded{
			Data:      buf.Bytes(),
			MediaType: "image/png",
			Ext:       "png",
		}, nil
	}

	// JPEG has no alpha channel, so anything transparent flattens first.
	if hasAlpha(img) {
		img = flattenOntoWhite(img)
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("images: encoding JPEG: %w", err)
	}
	return &Encoded{
		Data:      buf.Bytes(),
		MediaType: "image/jpeg",
		Ext:       "jpg",
	}, nil
}

// collectPalette gathers the image's distinct colours, giving up as soon as
// the limit is exceeded.
//
// Spec section 4.7 decides JPEG against PNG on unique-colour count. A
// photograph blows past the threshold within a few rows, so the early exit
// keeps this cheap on exactly the images where a full scan would be
// expensive. Returning the palette rather than only the count lets the caller
// write a paletted PNG, which is what makes the format choice pay off.
//
// ok is false when the image exceeded the limit, in which case the palette is
// nil.
func collectPalette(img image.Image, limit int) ([]color.Color, bool) {
	if limit <= 0 {
		return nil, false
	}
	if limit > 256 {
		// A PNG palette holds at most 256 entries.
		limit = 256
	}

	seen := make(map[uint32]struct{}, limit+1)
	// Order is the first-seen scan order, so the palette is deterministic.
	palette := make([]color.Color, 0, limit)

	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.At(x, y)
			r, g, bl, a := c.RGBA()
			key := uint32(r>>8)<<24 | uint32(g>>8)<<16 | uint32(bl>>8)<<8 | uint32(a>>8)
			if _, ok := seen[key]; ok {
				continue
			}
			if len(palette) >= limit {
				return nil, false
			}
			seen[key] = struct{}{}
			palette = append(palette, color.NRGBA{
				R: uint8(r >> 8), G: uint8(g >> 8),
				B: uint8(bl >> 8), A: uint8(a >> 8),
			})
		}
	}
	if len(palette) == 0 {
		return nil, false
	}
	return palette, true
}

// palettize converts an image to the given palette. Every colour in the
// palette came from the image, so this is lossless.
func palettize(img image.Image, palette []color.Color) *image.Paletted {
	b := img.Bounds()
	dst := image.NewPaletted(image.Rect(0, 0, b.Dx(), b.Dy()), palette)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// hasAlpha reports whether any pixel is not fully opaque.
func hasAlpha(img image.Image) bool {
	switch img.(type) {
	case *image.Gray, *image.Gray16, *image.YCbCr, *image.CMYK:
		return false
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xFFFF {
				return true
			}
		}
	}
	return false
}
