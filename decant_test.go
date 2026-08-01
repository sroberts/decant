package decant_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// fixedDate keeps every test's output timestamp stable.
var fixedDate = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// buildDoc converts a synthetic PDF and returns the EPUB bytes and report.
func buildDoc(t *testing.T, pdfBytes []byte, opts decant.Options) ([]byte, *decant.Report) {
	t.Helper()
	conv, err := decant.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var out bytes.Buffer
	rep, err := conv.Convert(context.Background(), bytes.NewReader(pdfBytes), int64(len(pdfBytes)), &out)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return out.Bytes(), rep
}

func defaultOpts() decant.Options {
	o := decant.DefaultOptions()
	o.Deterministic = fixedDate
	return o
}

// simpleDoc is a two-page document with plain paragraphs.
func simpleDoc() []byte {
	page1 := testpdf.TextPage("F1", 12, 72, 720, 14, []string{
		"The first paragraph begins here and continues across",
		"several lines of set text before it reaches its end.",
		"",
		"A second paragraph follows the blank line above, and it",
		"also runs to more than a single line of text.",
	})
	page2 := testpdf.TextPage("F1", 12, 72, 720, 14, []string{
		"The second page carries one more paragraph of body",
		"text so the converter has something to chapter.",
	})
	return testpdf.New().
		SetInfo("Title", "Test Document").
		SetInfo("Author", "A. Tester").
		AddPage(612, 792, page1).
		AddPage(612, 792, page2).
		Build()
}

func TestConvertProducesReadableText(t *testing.T) {
	data, rep := buildDoc(t, simpleDoc(), defaultOpts())

	if rep.PagesConverted != 2 {
		t.Errorf("PagesConverted = %d, want 2", rep.PagesConverted)
	}
	if rep.Chapters < 1 {
		t.Fatalf("Chapters = %d, want at least 1", rep.Chapters)
	}

	text := allChapterText(t, data)
	for _, want := range []string{
		"The first paragraph begins here",
		"A second paragraph follows",
		"The second page carries one more paragraph",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chapter text missing %q\ngot:\n%s", want, text)
		}
	}

	if r := rep.DecodeFailureRate(); r > 0 {
		t.Errorf("DecodeFailureRate = %v, want 0 for a base-14 font document", r)
	}
	if rep.QualityScore < 80 {
		t.Errorf("QualityScore = %d, want at least 80 for clean input", rep.QualityScore)
	}
}

func TestConvertSplitsParagraphs(t *testing.T) {
	data, _ := buildDoc(t, simpleDoc(), defaultOpts())
	text := allChapterText(t, data)

	// The blank line in the fixture must produce two separate <p> elements
	// rather than one run-on paragraph.
	if n := strings.Count(text, "<p"); n < 3 {
		t.Errorf("found %d paragraph elements, want at least 3\ngot:\n%s", n, text)
	}
	if strings.Contains(text, "its end. A second paragraph") {
		t.Error("paragraphs were merged across the blank line")
	}
}

func TestDeterministicOutput(t *testing.T) {
	src := simpleDoc()

	a, _ := buildDoc(t, src, defaultOpts())
	b, _ := buildDoc(t, src, defaultOpts())

	if !bytes.Equal(a, b) {
		t.Fatalf("two conversions of the same input differed: %d vs %d bytes", len(a), len(b))
	}

	// Spec section 9 requires byte-identical output across --jobs values.
	optsA := defaultOpts()
	optsA.Jobs = 1
	optsB := defaultOpts()
	optsB.Jobs = 16

	c, _ := buildDoc(t, src, optsA)
	d, _ := buildDoc(t, src, optsB)
	if !bytes.Equal(c, d) {
		t.Error("output differed between --jobs=1 and --jobs=16")
	}
}

func TestMimetypeEntryIsFirstAndStored(t *testing.T) {
	data, _ := buildDoc(t, simpleDoc(), defaultOpts())

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("opening EPUB: %v", err)
	}
	if len(zr.File) == 0 {
		t.Fatal("EPUB has no entries")
	}

	first := zr.File[0]
	if first.Name != "mimetype" {
		t.Errorf("first entry is %q, want \"mimetype\"", first.Name)
	}
	if first.Method != zip.Store {
		t.Errorf("mimetype method = %d, want Store (%d)", first.Method, zip.Store)
	}
	if len(first.Extra) != 0 {
		t.Errorf("mimetype carries %d bytes of extra field, want 0", len(first.Extra))
	}

	rc, err := first.Open()
	if err != nil {
		t.Fatalf("opening mimetype: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "application/epub+zip" {
		t.Errorf("mimetype content = %q", got)
	}
}

func TestRequiredEntriesPresent(t *testing.T) {
	data, _ := buildDoc(t, simpleDoc(), defaultOpts())
	names := entryNames(t, data)

	for _, want := range []string{
		"mimetype",
		"META-INF/container.xml",
		"OEBPS/package.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/toc.ncx",
		"OEBPS/styles/base.css",
		"OEBPS/text/ch001.xhtml",
	} {
		if !names[want] {
			t.Errorf("EPUB is missing %s", want)
		}
	}
}

func TestIdentifierIsStableAcrossRuns(t *testing.T) {
	src := simpleDoc()
	a, _ := buildDoc(t, src, defaultOpts())
	b, _ := buildDoc(t, src, defaultOpts())

	idA := extractIdentifier(t, a)
	idB := extractIdentifier(t, b)
	if idA != idB {
		t.Errorf("identifier changed between runs: %q vs %q", idA, idB)
	}
	if !strings.HasPrefix(idA, "urn:uuid:") {
		t.Errorf("identifier %q is not a urn:uuid", idA)
	}

	// A different input must yield a different identifier.
	other := testpdf.New().
		SetInfo("Title", "Different").
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 14, []string{"Other text entirely."})).
		Build()
	c, _ := buildDoc(t, other, defaultOpts())
	if extractIdentifier(t, c) == idA {
		t.Error("different inputs produced the same identifier")
	}
}

func TestMetadataOverrides(t *testing.T) {
	opts := defaultOpts()
	opts.Metadata = decant.Metadata{
		Title: "Override Title", Author: "Override Author", Language: "fr",
	}
	data, _ := buildDoc(t, simpleDoc(), opts)
	opf := entryContent(t, data, "OEBPS/package.opf")

	for _, want := range []string{
		"<dc:title>Override Title</dc:title>",
		"<dc:creator id=\"creator1\">Override Author</dc:creator>",
		"<dc:language>fr</dc:language>",
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("package.opf missing %q\ngot:\n%s", want, opf)
		}
	}
}

func TestMetadataFromPDFInfo(t *testing.T) {
	data, _ := buildDoc(t, simpleDoc(), defaultOpts())
	opf := entryContent(t, data, "OEBPS/package.opf")

	if !strings.Contains(opf, "<dc:title>Test Document</dc:title>") {
		t.Errorf("title was not taken from /Info\ngot:\n%s", opf)
	}
	if !strings.Contains(opf, "A. Tester") {
		t.Errorf("author was not taken from /Info\ngot:\n%s", opf)
	}
}

func TestEncryptedPDFIsRejected(t *testing.T) {
	// A trailer carrying /Encrypt is enough for the detection path; decant
	// exits before it would need to decrypt anything.
	src := simpleDoc()
	src = bytes.Replace(src,
		[]byte("trailer\n<< /Size"),
		[]byte("trailer\n<< /Encrypt 99 0 R /Size"), 1)

	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = conv.Convert(context.Background(), bytes.NewReader(src), int64(len(src)), io.Discard)
	if err == nil {
		t.Fatal("expected an error for an encrypted PDF")
	}

	var enc *decant.EncryptedError
	if !errors.As(err, &enc) {
		t.Fatalf("error is %T (%v), want *decant.EncryptedError", err, err)
	}
}

func TestScannedPDFIsRejected(t *testing.T) {
	// Pages with almost no text. Without image XObjects the classifier must
	// not fire, because both conditions are required.
	b := testpdf.New()
	for i := 0; i < 10; i++ {
		b.AddPage(612, 792, testpdf.TextPage("F1", 10, 72, 720, 12, []string{"3"}))
	}
	src := b.Build()

	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = conv.Convert(context.Background(), bytes.NewReader(src), int64(len(src)), io.Discard)

	var scan *decant.NoTextLayerError
	if errors.As(err, &scan) {
		t.Fatal("classifier fired on a text document with no images; " +
			"both conditions in spec section 6 are required")
	}
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
}

func TestPageRangeSelection(t *testing.T) {
	pr, err := decant.ParsePageRange("2")
	if err != nil {
		t.Fatalf("ParsePageRange: %v", err)
	}
	opts := defaultOpts()
	opts.Pages = pr

	data, rep := buildDoc(t, simpleDoc(), opts)
	if rep.PagesConverted != 1 {
		t.Errorf("PagesConverted = %d, want 1", rep.PagesConverted)
	}

	text := allChapterText(t, data)
	if strings.Contains(text, "The first paragraph begins") {
		t.Error("page 1 content leaked into a conversion restricted to page 2")
	}
	if !strings.Contains(text, "The second page carries") {
		t.Error("page 2 content is missing")
	}
}

func TestChunkSizeSplitsChapters(t *testing.T) {
	// Enough text that a small chunk limit must split it. The blank lines
	// matter: splitting is only permitted at a paragraph boundary, so a
	// single enormous paragraph would legitimately stay in one file.
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines,
			"This is a line of body text used to grow the document past the chunk limit.",
			"It continues onto a second line so the paragraph has some substance to it.",
			"",
		)
	}
	src := testpdf.New().
		AddPage(612, 2000, testpdf.TextPage("F1", 10, 72, 1950, 14, lines)).
		Build()

	opts := defaultOpts()
	opts.MaxChunkBytes = 4096
	opts.SplitAt = decant.SplitAtNone

	data, rep := buildDoc(t, src, opts)
	if rep.Chapters < 2 {
		t.Fatalf("Chapters = %d, want at least 2 with a 4096 byte limit", rep.Chapters)
	}

	names := entryNames(t, data)
	if !names["OEBPS/text/ch001-2.xhtml"] {
		t.Error("expected a ch001-2.xhtml continuation file")
	}
	if rep.LargestChapterBytes > opts.MaxChunkBytes {
		t.Errorf("largest chapter body is %d bytes, above the %d limit",
			rep.LargestChapterBytes, opts.MaxChunkBytes)
	}
}

func TestProfileDefaults(t *testing.T) {
	opts := decant.DefaultOptions()
	opts.Profile = decant.ProfileCrossPoint
	opts.ApplyProfileDefaults()

	if opts.MaxChunkBytes != 65536 {
		t.Errorf("crosspoint MaxChunkBytes = %d, want 65536", opts.MaxChunkBytes)
	}
	if opts.ImageMaxWidth != 480 {
		t.Errorf("crosspoint ImageMaxWidth = %d, want 480", opts.ImageMaxWidth)
	}
	if opts.Images != decant.ImagesGrayscale {
		t.Errorf("crosspoint Images = %q, want grayscale", opts.Images)
	}

	min := decant.DefaultOptions()
	min.Profile = decant.ProfileMinimal
	min.ApplyProfileDefaults()
	if min.Images != decant.ImagesDrop {
		t.Errorf("minimal Images = %q, want drop", min.Images)
	}
}

func TestMinimalProfileOmitsStylesheet(t *testing.T) {
	opts := defaultOpts()
	opts.Profile = decant.ProfileMinimal
	opts.ApplyProfileDefaults()

	data, _ := buildDoc(t, simpleDoc(), opts)
	names := entryNames(t, data)
	if names["OEBPS/styles/base.css"] {
		t.Error("minimal profile emitted a stylesheet")
	}

	ch := entryContent(t, data, "OEBPS/text/ch001.xhtml")
	if strings.Contains(ch, "stylesheet") {
		t.Error("chapter links a stylesheet that is not in the container")
	}
}

func TestAnalyzeExposesBlockTree(t *testing.T) {
	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	src := simpleDoc()
	doc, err := conv.Analyze(context.Background(), bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(doc.Blocks) == 0 {
		t.Fatal("Analyze produced no blocks")
	}

	// The split between Analyze and Write exists so callers can correct
	// structure first. Promoting a block to a heading must reach the output.
	doc.Blocks[0].Kind = decant.KindHeading
	doc.Blocks[0].Level = 1
	doc.Blocks[0].Text = "Corrected Heading"

	var out bytes.Buffer
	if _, err := conv.Write(context.Background(), doc, &out); err != nil {
		t.Fatalf("Write: %v", err)
	}
	text := allChapterText(t, out.Bytes())
	if !strings.Contains(text, "<h1") || !strings.Contains(text, "Corrected Heading") {
		t.Errorf("caller's edit did not reach the output\ngot:\n%s", text)
	}
}

func TestBlockIDsAreStableAndUnique(t *testing.T) {
	conv, _ := decant.New(defaultOpts())
	src := simpleDoc()

	ids := func() []string {
		doc, err := conv.Analyze(context.Background(), bytes.NewReader(src), int64(len(src)))
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		out := make([]string, len(doc.Blocks))
		for i, b := range doc.Blocks {
			out[i] = b.ID
		}
		return out
	}

	a, b := ids(), ids()
	if len(a) != len(b) {
		t.Fatalf("block count changed between runs: %d vs %d", len(a), len(b))
	}
	seen := map[string]bool{}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("block %d id changed between runs: %q vs %q", i, a[i], b[i])
		}
		if a[i] == "" {
			t.Errorf("block %d has an empty id", i)
		}
		if seen[a[i]] {
			t.Errorf("duplicate block id %q", a[i])
		}
		seen[a[i]] = true
	}
}

func TestXMLIsWellFormed(t *testing.T) {
	// Text carrying XML metacharacters must not break the output.
	src := testpdf.New().
		SetInfo("Title", `Ampersands & <angles> "quotes"`).
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 14, []string{
			`A line with & ampersand, <tag>, and "quotes".`,
		})).
		Build()

	data, _ := buildDoc(t, src, defaultOpts())
	for _, name := range []string{
		"META-INF/container.xml", "OEBPS/package.opf",
		"OEBPS/nav.xhtml", "OEBPS/toc.ncx", "OEBPS/text/ch001.xhtml",
	} {
		content := entryContent(t, data, name)
		if err := checkWellFormed(content); err != nil {
			t.Errorf("%s is not well-formed XML: %v", name, err)
		}
	}
}

func TestRotatedPageExtractsText(t *testing.T) {
	// A /Rotate 90 page whose text is pre-rotated in user space, which is how
	// real landscape pages are produced. After the base CTM applies /Rotate,
	// the text must come out upright and in order.
	content := testpdf.RotatedTextPage("F1", 12, 100, 72, 14, []string{
		"Rotated page line one.",
		"Rotated page line two.",
	})
	src := testpdf.New().AddRotatedPage(612, 792, 90, content).Build()

	data, rep := buildDoc(t, src, defaultOpts())
	for _, p := range rep.Pages {
		if p.RotatedDropped > 0 {
			t.Errorf("dropped %d run(s) as rotated; pre-rotated text on a "+
				"/Rotate 90 page should read upright", p.RotatedDropped)
		}
	}

	text := allChapterText(t, data)
	if !strings.Contains(text, "Rotated page line one") {
		t.Errorf("rotated page text missing\ngot:\n%s", text)
	}
	if i, j := strings.Index(text, "line one"), strings.Index(text, "line two"); i < 0 || i > j {
		t.Error("rotated page lines came out in the wrong order")
	}
}

func TestRotatedRunsAreDropped(t *testing.T) {
	// The complementary case: text that really is sideways relative to the
	// displayed page is margin furniture and drops with a diagnostic, per
	// spec section 4.3.
	body := testpdf.TextPage("F1", 12, 72, 720, 14, []string{"Upright body text here."})
	sideways := testpdf.RotatedTextPage("F1", 8, 30, 300, 10, []string{"Sideways margin note"})
	src := testpdf.New().AddPage(612, 792, body+sideways).Build()

	data, rep := buildDoc(t, src, defaultOpts())
	text := allChapterText(t, data)

	if !strings.Contains(text, "Upright body text") {
		t.Errorf("body text missing\ngot:\n%s", text)
	}
	if strings.Contains(text, "Sideways margin note") {
		t.Error("sideways run was kept; spec 4.3 drops rotated runs by default")
	}
	if rep.Pages[0].RotatedDropped == 0 {
		t.Error("dropping the sideways run was not recorded in the report")
	}
}

func TestOutlineIsExtracted(t *testing.T) {
	src := testpdf.New().
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 14, []string{"Chapter one body."})).
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 14, []string{"Chapter two body."})).
		AddBookmark("Chapter One", 0, 750).
		AddBookmark("Chapter Two", 1, 750).
		Build()

	conv, _ := decant.New(defaultOpts())
	doc, err := conv.Analyze(context.Background(), bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(doc.Outline) != 2 {
		t.Fatalf("Outline has %d entries, want 2: %+v", len(doc.Outline), doc.Outline)
	}
	if doc.Outline[0].Title != "Chapter One" {
		t.Errorf("first outline title = %q", doc.Outline[0].Title)
	}
	if doc.Outline[1].Page != 1 {
		t.Errorf("second outline destination page = %d, want 1", doc.Outline[1].Page)
	}
}

// --- helpers ---

func entryNames(t *testing.T, epubBytes []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(epubBytes), int64(len(epubBytes)))
	if err != nil {
		t.Fatalf("opening EPUB: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return names
}

func entryContent(t *testing.T, epubBytes []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(epubBytes), int64(len(epubBytes)))
	if err != nil {
		t.Fatalf("opening EPUB: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", name, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		return string(b)
	}
	t.Fatalf("EPUB has no entry named %s", name)
	return ""
}

func allChapterText(t *testing.T, epubBytes []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(epubBytes), int64(len(epubBytes)))
	if err != nil {
		t.Fatalf("opening EPUB: %v", err)
	}
	var sb strings.Builder
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "OEBPS/text/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		sb.Write(b)
	}
	return sb.String()
}

func extractIdentifier(t *testing.T, epubBytes []byte) string {
	t.Helper()
	opf := entryContent(t, epubBytes, "OEBPS/package.opf")
	const open = `<dc:identifier id="pub-id">`
	i := strings.Index(opf, open)
	if i < 0 {
		t.Fatalf("no dc:identifier in package.opf:\n%s", opf)
	}
	rest := opf[i+len(open):]
	j := strings.Index(rest, "<")
	if j < 0 {
		t.Fatal("unterminated dc:identifier")
	}
	return rest[:j]
}
