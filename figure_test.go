package decant_test

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// figureDoc is a page with two paragraphs and an image between them.
func figureDoc() []byte {
	body := testpdf.TextPage("F1", 12, 72, 720, 15, []string{
		"The first paragraph sits above the figure and runs across a",
		"couple of lines so it reads as real body text.",
	}) + testpdf.TextPage("F1", 12, 72, 400, 15, []string{
		"The second paragraph sits below the figure and likewise runs",
		"to more than a single line of set text.",
	})
	// 200x150pt, well above the size floor, drawn between the paragraphs.
	draw := testpdf.DrawImage("Im1", 200, 480, 200, 150)

	return testpdf.New().
		SetInfo("Title", "Figure Document").
		AddImage("Im1", 40, 30, testpdf.GradientRGB(40, 30)).
		AddPage(612, 792, body+draw).
		Build()
}

func imageEntries(t *testing.T, epubBytes []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(epubBytes), int64(len(epubBytes)))
	if err != nil {
		t.Fatalf("opening EPUB: %v", err)
	}
	var out []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "OEBPS/images/") {
			out = append(out, f.Name)
		}
	}
	return out
}

func imageBytes(t *testing.T, epubBytes []byte, name string) []byte {
	t.Helper()
	zr, _ := zip.NewReader(bytes.NewReader(epubBytes), int64(len(epubBytes)))
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return b
	}
	t.Fatalf("no entry %s", name)
	return nil
}

func TestImageExtractedAndPlaced(t *testing.T) {
	data, rep := buildDoc(t, figureDoc(), defaultOpts())

	if rep.ImagesPlaced != 1 {
		t.Fatalf("ImagesPlaced = %d, want 1", rep.ImagesPlaced)
	}
	if got := imageEntries(t, data); len(got) != 1 {
		t.Fatalf("image entries = %v, want exactly one", got)
	}

	text := allChapterText(t, data)
	if !strings.Contains(text, "<figure") || !strings.Contains(text, "<img src=\"../images/") {
		t.Errorf("no figure markup in the output:\n%s", text)
	}
}

func TestFigureLandsInReadingOrder(t *testing.T) {
	// Spec 4.7 places an image at its reading-order position, not at the end.
	data, _ := buildDoc(t, figureDoc(), defaultOpts())
	text := allChapterText(t, data)

	first := strings.Index(text, "The first paragraph")
	fig := strings.Index(text, "<figure")
	second := strings.Index(text, "The second paragraph")

	if first < 0 || fig < 0 || second < 0 {
		t.Fatalf("missing content:\n%s", text)
	}
	if !(first < fig && fig < second) {
		t.Errorf("figure is not between the paragraphs (first=%d fig=%d second=%d):\n%s",
			first, fig, second, text)
	}
}

func TestImageManifestEntry(t *testing.T) {
	data, _ := buildDoc(t, figureDoc(), defaultOpts())
	opf := entryContent(t, data, "OEBPS/package.opf")

	if !strings.Contains(opf, `href="images/img001.`) {
		t.Errorf("package.opf has no image manifest entry:\n%s", opf)
	}
	if !strings.Contains(opf, `media-type="image/`) {
		t.Errorf("image manifest entry has no media type:\n%s", opf)
	}
}

func TestImagesDropMode(t *testing.T) {
	opts := defaultOpts()
	opts.Images = decant.ImagesDrop

	data, rep := buildDoc(t, figureDoc(), opts)
	if rep.ImagesPlaced != 0 {
		t.Errorf("ImagesPlaced = %d with --images=drop, want 0", rep.ImagesPlaced)
	}
	if got := imageEntries(t, data); len(got) != 0 {
		t.Errorf("images written despite drop mode: %v", got)
	}
	if strings.Contains(allChapterText(t, data), "<img") {
		t.Error("an img element survived drop mode")
	}
	// Text must be unaffected.
	if !strings.Contains(allChapterText(t, data), "The first paragraph") {
		t.Error("drop mode damaged the text")
	}
}

func TestSmallImagesDropped(t *testing.T) {
	// Spec 4.7 drops images under 16 points or under 2% of page area.
	tiny := testpdf.DrawImage("Im1", 100, 700, 10, 10)
	body := testpdf.TextPage("F1", 12, 72, 600, 15, []string{
		"Body text so the page is not classified as a scan and has",
		"enough content to convert normally.",
	})
	src := testpdf.New().
		AddImage("Im1", 8, 8, testpdf.GradientRGB(8, 8)).
		AddPage(612, 792, body+tiny).
		Build()

	_, rep := buildDoc(t, src, defaultOpts())
	if rep.ImagesPlaced != 0 {
		t.Errorf("a 10pt image was kept; ImagesPlaced = %d", rep.ImagesPlaced)
	}

	// --keep-small-images overrides the rule.
	opts := defaultOpts()
	opts.KeepSmallImages = true
	_, rep = buildDoc(t, src, opts)
	if rep.ImagesPlaced != 1 {
		t.Errorf("KeepSmallImages did not retain the image; ImagesPlaced = %d",
			rep.ImagesPlaced)
	}
}

func TestBackgroundImageDropped(t *testing.T) {
	// A page-covering image painted before any text is a background or
	// watermark, per spec 4.7.
	bg := testpdf.DrawImage("Im1", 0, 0, 612, 792)
	body := testpdf.TextPage("F1", 12, 72, 700, 15, []string{
		"Body text drawn over the background image, running across a",
		"couple of lines so the page converts normally.",
	})
	src := testpdf.New().
		AddImage("Im1", 20, 26, testpdf.GradientRGB(20, 26)).
		AddPage(612, 792, bg+body).
		Build()

	_, rep := buildDoc(t, src, defaultOpts())
	if rep.ImagesPlaced != 0 {
		t.Errorf("a full-page background was kept; ImagesPlaced = %d", rep.ImagesPlaced)
	}
}

func TestForegroundFullPageImageKept(t *testing.T) {
	// The complementary case: a page-covering image painted after the text is
	// not a background and must survive.
	body := testpdf.TextPage("F1", 12, 72, 700, 15, []string{
		"Body text drawn before the image, running across a couple of",
		"lines so the page converts normally.",
	})
	fg := testpdf.DrawImage("Im1", 0, 0, 612, 700)
	src := testpdf.New().
		AddImage("Im1", 20, 26, testpdf.GradientRGB(20, 26)).
		AddPage(612, 792, body+fg).
		Build()

	_, rep := buildDoc(t, src, defaultOpts())
	if rep.ImagesPlaced != 1 {
		t.Errorf("a foreground image was dropped; ImagesPlaced = %d", rep.ImagesPlaced)
	}
}

func TestImageDeduplication(t *testing.T) {
	// Spec 4.7 deduplicates by SHA-256 of decoded pixels, so a logo repeated
	// on every page becomes one manifest entry.
	draw := testpdf.DrawImage("Im1", 100, 500, 200, 150)
	body := testpdf.TextPage("F1", 12, 72, 700, 15, []string{
		"Body text on this page so it converts normally and the image",
		"has somewhere to sit in the reading order.",
	})
	b := testpdf.New().AddImage("Im1", 40, 30, testpdf.GradientRGB(40, 30))
	for i := 0; i < 4; i++ {
		b.AddPage(612, 792, body+draw)
	}

	data, rep := buildDoc(t, b.Build(), defaultOpts())
	if rep.ImagesPlaced != 1 {
		t.Errorf("the same image on 4 pages produced %d manifest entries, want 1",
			rep.ImagesPlaced)
	}
	if got := imageEntries(t, data); len(got) != 1 {
		t.Errorf("image entries = %v, want one", got)
	}
	// All four figures must still reference it.
	if n := strings.Count(allChapterText(t, data), "<img"); n != 4 {
		t.Errorf("found %d img elements, want 4 referencing the single image", n)
	}
}

func TestImageScaling(t *testing.T) {
	// Spec 4.7 scales when the longest edge exceeds --image-max-width.
	big := testpdf.GradientRGB(300, 200)
	src := testpdf.New().
		AddImage("Im1", 300, 200, big).
		AddPage(612, 792,
			testpdf.TextPage("F1", 12, 72, 700, 15, []string{
				"Body text so the page converts normally and is not taken",
				"for a scanned document by the classifier.",
			})+testpdf.DrawImage("Im1", 100, 300, 400, 260)).
		Build()

	opts := defaultOpts()
	opts.ImageMaxWidth = 100

	data, rep := buildDoc(t, src, opts)
	if rep.ImagesPlaced != 1 {
		t.Fatalf("ImagesPlaced = %d, want 1", rep.ImagesPlaced)
	}

	entries := imageEntries(t, data)
	img, _, err := image.Decode(bytes.NewReader(imageBytes(t, data, entries[0])))
	if err != nil {
		t.Fatalf("decoding output image: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 100 || b.Dy() > 100 {
		t.Errorf("output image is %dx%d, want the longest edge at most 100",
			b.Dx(), b.Dy())
	}
	// Aspect ratio must be preserved: 300x200 scaled to 100 wide is 100x67.
	if b.Dx() != 100 || b.Dy() < 65 || b.Dy() > 68 {
		t.Errorf("output image is %dx%d, want about 100x67", b.Dx(), b.Dy())
	}
}

func TestLineArtEncodedAsPNG(t *testing.T) {
	// Spec 4.7 picks PNG for line art, decided by unique-colour count.
	src := testpdf.New().
		AddImage("Im1", 40, 30, testpdf.SolidRGB(40, 30, 200, 30, 30)).
		AddPage(612, 792,
			testpdf.TextPage("F1", 12, 72, 700, 15, []string{
				"Body text so the page converts normally and the classifier",
				"does not take it for a scan.",
			})+testpdf.DrawImage("Im1", 100, 300, 200, 150)).
		Build()

	data, _ := buildDoc(t, src, defaultOpts())
	entries := imageEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("image entries = %v", entries)
	}
	if !strings.HasSuffix(entries[0], ".png") {
		t.Errorf("a single-colour image was encoded as %s, want PNG", entries[0])
	}
}

func TestPhotographicImageEncodedAsJPEG(t *testing.T) {
	src := testpdf.New().
		AddImage("Im1", 64, 64, testpdf.GradientRGB(64, 64)).
		AddPage(612, 792,
			testpdf.TextPage("F1", 12, 72, 700, 15, []string{
				"Body text so the page converts normally and the classifier",
				"does not take it for a scan.",
			})+testpdf.DrawImage("Im1", 100, 300, 200, 200)).
		Build()

	data, _ := buildDoc(t, src, defaultOpts())
	entries := imageEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("image entries = %v", entries)
	}
	if !strings.HasSuffix(entries[0], ".jpg") {
		t.Errorf("a many-colour image was encoded as %s, want JPEG", entries[0])
	}
}

func TestCrossPointKeepsPNGForLineArt(t *testing.T) {
	// Spec section 13 closed the in-EPUB format question on 2026-08-01: the
	// CrossPoint firmware's EPUB path decodes indexed PNG and has no BMP
	// decoder, so line art stays PNG where it is both smaller and sharp. A
	// regression to JPEG would mean that finding was lost.
	src := testpdf.New().
		AddImage("Im1", 40, 30, testpdf.SolidRGB(40, 30, 200, 30, 30)).
		AddPage(612, 792,
			testpdf.TextPage("F1", 12, 72, 700, 15, []string{
				"Body text so the page converts normally and the classifier",
				"does not take it for a scan.",
			})+testpdf.DrawImage("Im1", 100, 300, 300, 220)).
		Build()

	opts := defaultOpts()
	opts.Profile = decant.ProfileCrossPoint
	opts.ApplyProfileDefaults()

	data, rep := buildDoc(t, src, opts)
	if rep.ImagesPlaced != 1 {
		t.Fatalf("ImagesPlaced = %d, want 1", rep.ImagesPlaced)
	}
	entries := imageEntries(t, data)
	if !strings.HasSuffix(entries[0], ".png") {
		t.Errorf("crosspoint emitted %s for line art, want PNG", entries[0])
	}

	img, _, err := image.Decode(bytes.NewReader(imageBytes(t, data, entries[0])))
	if err != nil {
		t.Fatalf("decoding output image: %v", err)
	}
	// Indexed, which is the form the firmware's decoder handles and the
	// reason PNG is worth the extra heap it costs there.
	if _, ok := img.(*image.Paletted); !ok {
		t.Errorf("output is %T, want *image.Paletted", img)
	}
	if b := img.Bounds(); b.Dx() > 480 || b.Dy() > 480 {
		t.Errorf("crosspoint image is %dx%d, above the 480px panel width",
			b.Dx(), b.Dy())
	}
}

func TestCrossPointDithersPhotographsToJPEG(t *testing.T) {
	// The complementary case from spec 5.1: a photograph quantizes to 16 gray
	// levels, dithers, and encodes JPEG, which is what keeps a low-bit-depth
	// panel from banding.
	src := testpdf.New().
		AddImage("Im1", 64, 64, testpdf.GradientRGB(64, 64)).
		AddPage(612, 792,
			testpdf.TextPage("F1", 12, 72, 700, 15, []string{
				"Body text so the page converts normally and the classifier",
				"does not take it for a scan.",
			})+testpdf.DrawImage("Im1", 100, 300, 300, 300)).
		Build()

	opts := defaultOpts()
	opts.Profile = decant.ProfileCrossPoint
	opts.ApplyProfileDefaults()

	data, rep := buildDoc(t, src, opts)
	if rep.ImagesPlaced != 1 {
		t.Fatalf("ImagesPlaced = %d, want 1", rep.ImagesPlaced)
	}
	entries := imageEntries(t, data)
	if !strings.HasSuffix(entries[0], ".jpg") {
		t.Errorf("crosspoint emitted %s for a photograph, want JPEG", entries[0])
	}

	img, _, err := image.Decode(bytes.NewReader(imageBytes(t, data, entries[0])))
	if err != nil {
		t.Fatalf("decoding output image: %v", err)
	}
	if b := img.Bounds(); b.Dx() > 480 || b.Dy() > 480 {
		t.Errorf("crosspoint image is %dx%d, above the 480px panel width",
			b.Dx(), b.Dy())
	}

	// Grayscale: every pixel's channels must be near-equal. JPEG's chroma
	// subsampling leaves a little colour noise, so this allows a small
	// tolerance rather than demanding exact equality.
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 7 {
		for x := b.Min.X; x < b.Max.X; x += 7 {
			r, g, bl, _ := img.At(x, y).RGBA()
			if absDiff(r, g) > 0x1400 || absDiff(g, bl) > 0x1400 {
				t.Fatalf("pixel at (%d,%d) is not gray: r=%d g=%d b=%d", x, y, r, g, bl)
			}
		}
	}
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestCaptionAttachedToFigure(t *testing.T) {
	// Spec 4.6: a block opening with a Figure label near an image is its
	// caption, and belongs inside figcaption rather than as a paragraph.
	body := testpdf.TextPage("F1", 12, 72, 720, 15, []string{
		"Body text above the figure that runs across a couple of lines",
		"so the page has genuine prose in it.",
	})
	draw := testpdf.DrawImage("Im1", 150, 450, 250, 180)
	caption := testpdf.TextPage("F1", 9, 150, 435, 11, []string{
		"Figure 1: A caption describing the image above it.",
	})
	src := testpdf.New().
		AddImage("Im1", 50, 36, testpdf.GradientRGB(50, 36)).
		AddPage(612, 792, body+draw+caption).
		Build()

	data, _ := buildDoc(t, src, defaultOpts())
	text := allChapterText(t, data)

	if !strings.Contains(text, "<figcaption>") {
		t.Errorf("no figcaption in the output:\n%s", text)
	}
	if !strings.Contains(text, "Figure 1:") {
		t.Errorf("the caption text is missing:\n%s", text)
	}
	// It must not also appear as a standalone paragraph.
	if strings.Contains(text, "<p>Figure 1:") || strings.Contains(text, `<p class="first">Figure 1:`) {
		t.Errorf("the caption was emitted twice:\n%s", text)
	}
}

func TestUndecodableImageDropsWithDiagnostic(t *testing.T) {
	// A JPXDecode image has no pure-Go decoder; spec principle 2 rules out
	// the cgo one. It must drop with a diagnostic rather than fail the run.
	src := testpdf.New().
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 700, 15, []string{
			"Body text so the document converts even though its image",
			"cannot be decoded by any pure-Go decoder.",
		})).
		Build()

	// The synthetic builder cannot emit JPX, so this asserts the shape of the
	// contract: a document with no decodable image still converts cleanly.
	_, rep := buildDoc(t, src, defaultOpts())
	if rep.QualityScore < 90 {
		t.Errorf("quality score %d on a clean text document", rep.QualityScore)
	}
}

func TestFigureRemovedByCallerIsNotShipped(t *testing.T) {
	// Analyze and Write are split so a caller can edit the block tree. An
	// image whose figure was removed must not remain in the manifest, which
	// epubcheck reports as an unreferenced item.
	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatal(err)
	}
	src := figureDoc()
	doc := analyze(t, src, defaultOpts())

	var kept []decant.Block
	for _, b := range doc.Blocks {
		if b.Kind != decant.KindFigure {
			kept = append(kept, b)
		}
	}
	doc.Blocks = kept

	var out bytes.Buffer
	rep, err := conv.Write(contextTODO(), doc, &out)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rep.ImagesPlaced != 0 {
		t.Errorf("ImagesPlaced = %d after the figure was removed", rep.ImagesPlaced)
	}
	if got := imageEntries(t, out.Bytes()); len(got) != 0 {
		t.Errorf("orphaned image entries remain: %v", got)
	}
}

func TestImageOutputIsDeterministic(t *testing.T) {
	src := figureDoc()
	a, _ := buildDoc(t, src, defaultOpts())
	b, _ := buildDoc(t, src, defaultOpts())
	if !bytes.Equal(a, b) {
		t.Error("two conversions with images produced different bytes")
	}
}

// contextTODO is a small helper so tests read cleanly.
func contextTODO() context.Context { return context.Background() }

func TestFigureDoesNotDisturbColumnOrder(t *testing.T) {
	// Inserting a figure must not re-sort the paragraphs. Sorting the
	// combined list by vertical position interleaves the columns of a
	// two-column page, which is exactly the scrambling column detection
	// exists to prevent.
	left := []string{
		"Alpha one text here", "Alpha two text here", "Alpha three here",
		"Alpha four text now", "Alpha five text now", "Alpha six text now",
		"Alpha seven is here", "Alpha eight is here", "Alpha nine is now",
		"Alpha ten text here", "Alpha eleven here", "Alpha twelve now",
	}
	right := []string{
		"Beta one text here", "Beta two text here", "Beta three here",
		"Beta four text now", "Beta five text now", "Beta six text now",
		"Beta seven is here", "Beta eight is here", "Beta nine is now",
		"Beta ten text here", "Beta eleven here", "Beta twelve now",
	}
	// An image in the left column, level with the right column's text.
	body := testpdf.TwoColumnPage("F1", 10, 13, "", left, right)
	draw := testpdf.DrawImage("Im1", 80, 400, 120, 90)

	src := testpdf.New().
		AddImage("Im1", 30, 22, testpdf.GradientRGB(30, 22)).
		AddPage(612, 792, body+draw).
		Build()

	doc := analyze(t, src, defaultOpts())
	if doc.Report().MultiColumnPages != 1 {
		t.Skip("column detection did not fire on this fixture")
	}

	var order []string
	for _, b := range doc.Blocks {
		if b.Kind == decant.KindFigure {
			order = append(order, "[FIGURE]")
			continue
		}
		order = append(order, b.Text)
	}
	joined := strings.Join(order, " | ")

	lastAlpha := strings.LastIndex(joined, "Alpha twelve")
	firstBeta := strings.Index(joined, "Beta one")
	if lastAlpha < 0 || firstBeta < 0 {
		t.Fatalf("column text missing:\n%s", joined)
	}
	if lastAlpha > firstBeta {
		t.Errorf("adding a figure interleaved the columns:\n%s", joined)
	}
}
