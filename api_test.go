package decant_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// The tests here pin the public contract that spec section 7 promises and
// that the CrossPoint TUI consumes. They are deliberately about behaviour a
// caller can depend on rather than about conversion quality: each one exists
// because a plausible refactor could break it silently, and every break would
// be a breaking API change rather than a heuristic drift.

func apiDoc() []byte {
	return testpdf.New().AddPage(612, 792, testpdf.HeadingPage("F1", 11, 18, 14, [][]string{
		{"First Section", "Body text for the first section running to a second line here."},
		{"Second Section", "Body text for the second section running to a second line here."},
	})).Build()
}

func analyzed(t *testing.T, opts decant.Options) (*decant.Converter, *decant.Document) {
	t.Helper()
	conv, err := decant.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	src := apiDoc()
	doc, err := conv.Analyze(context.Background(), bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return conv, doc
}

func TestWriteDoesNotConsumeTheDocument(t *testing.T) {
	// The TUI writes a preview, lets the user keep editing, then writes
	// again. If Write mutated the tree or accumulated into the report, the
	// second write would differ from the first for no reason the caller can
	// see.
	conv, doc := analyzed(t, defaultOpts())

	var first, second bytes.Buffer
	r1, err := conv.Write(context.Background(), doc, &first)
	if err != nil {
		t.Fatalf("first Write: %v", err)
	}
	chapters, quality := r1.Chapters, r1.QualityScore

	r2, err := conv.Write(context.Background(), doc, &second)
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}

	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Errorf("writing the same document twice produced different bytes: %d vs %d",
			first.Len(), second.Len())
	}
	if r2.Chapters != chapters {
		t.Errorf("the report accumulated across writes: %d chapters then %d",
			chapters, r2.Chapters)
	}
	if r2.QualityScore != quality {
		t.Errorf("quality score drifted across writes: %d then %d",
			quality, r2.QualityScore)
	}
}

func TestHeadingLevelIsClampedNotRejected(t *testing.T) {
	// A TUI editing heading levels can easily produce one out of range, and
	// XHTML has only h1 through h6. Clamping keeps a stray edit from either
	// failing the conversion or emitting an element that does not exist.
	for _, tc := range []struct {
		level int
		want  string
	}{
		{-1, "<h1"},
		{0, "<h1"},
		{1, "<h1"},
		{6, "<h6"},
		{7, "<h6"},
		{99, "<h6"},
	} {
		conv, doc := analyzed(t, defaultOpts())
		edited := false
		for i := range doc.Blocks {
			if doc.Blocks[i].Kind == decant.KindHeading {
				doc.Blocks[i].Level = tc.level
				edited = true
				break
			}
		}
		if !edited {
			t.Fatal("fixture produced no heading to edit")
		}

		var out bytes.Buffer
		if _, err := conv.Write(context.Background(), doc, &out); err != nil {
			t.Errorf("level %d: Write failed: %v", tc.level, err)
			continue
		}

		text := allChapterText(t, out.Bytes())
		if !strings.Contains(text, tc.want) {
			t.Errorf("level %d did not clamp to %s", tc.level, tc.want)
		}
		for _, bad := range regexp.MustCompile(`<h(0|-\d|[7-9]|\d\d+)\b`).FindAllString(text, -1) {
			t.Errorf("level %d emitted %s, which is not an XHTML heading", tc.level, bad)
		}
	}
}

func TestDeletingABlockIsSupported(t *testing.T) {
	// Dropping a block is how a caller removes a stray artifact. It must not
	// leave a dangling reference behind in the nav or the manifest.
	conv, doc := analyzed(t, defaultOpts())
	if len(doc.Blocks) < 2 {
		t.Fatalf("fixture produced %d blocks, need at least 2", len(doc.Blocks))
	}
	dropped := doc.Blocks[len(doc.Blocks)-1].Text
	doc.Blocks = doc.Blocks[:len(doc.Blocks)-1]

	var out bytes.Buffer
	if _, err := conv.Write(context.Background(), doc, &out); err != nil {
		t.Fatalf("Write after deleting a block: %v", err)
	}
	if dropped != "" && strings.Contains(allChapterText(t, out.Bytes()), dropped) {
		t.Error("the deleted block still reached the output")
	}
	if err := checkWellFormed(entryContent(t, out.Bytes(), "OEBPS/nav.xhtml")); err != nil {
		t.Errorf("nav is malformed after a deletion: %v", err)
	}
}

func TestEmptyDocumentIsRefused(t *testing.T) {
	// Principle 3: never emit silently corrupt EPUB. A document with no
	// blocks would serialize to a valid container with no content.
	conv, doc := analyzed(t, defaultOpts())
	doc.Blocks = nil

	var out bytes.Buffer
	if _, err := conv.Write(context.Background(), doc, &out); err == nil {
		t.Error("an empty document wrote an EPUB instead of failing")
	}
	if out.Len() != 0 {
		t.Errorf("a refused Write still emitted %d bytes", out.Len())
	}
}

func TestNilDocumentIsAUsageError(t *testing.T) {
	conv, _ := analyzed(t, defaultOpts())
	var out bytes.Buffer
	_, err := conv.Write(context.Background(), nil, &out)
	var ue *decant.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("Write(nil) returned %v, want a *UsageError", err)
	}
}

func TestZeroOptionsIsRefused(t *testing.T) {
	// Options documents that its zero value is not usable. New must say so
	// rather than converting with empty modes.
	if _, err := decant.New(decant.Options{}); err == nil {
		t.Error("New accepted a zero Options")
	}
}

func TestConverterIsReusableAcrossDocuments(t *testing.T) {
	// Converter documents that it holds no mutable state. A TUI keeps one
	// for a whole library, so leakage between documents would corrupt the
	// second conversion in a way that only shows up under load.
	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatal(err)
	}

	convert := func(src []byte) []byte {
		var out bytes.Buffer
		if _, err := conv.Convert(context.Background(), bytes.NewReader(src), int64(len(src)), &out); err != nil {
			t.Fatalf("Convert: %v", err)
		}
		return out.Bytes()
	}

	a := apiDoc()
	b := simpleDoc()
	first := convert(a)
	convert(b)
	again := convert(a)

	if !bytes.Equal(first, again) {
		t.Error("converting a second document changed the result of reconverting the first")
	}
}

func TestOptionsAreCopiedNotAliased(t *testing.T) {
	// New must not retain a reference the caller can mutate underneath it.
	opts := defaultOpts()
	conv, err := decant.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	before := conv.Options().MaxChunkBytes

	opts.MaxChunkBytes = 1
	if got := conv.Options().MaxChunkBytes; got != before {
		t.Errorf("mutating the caller's Options changed the converter: %d -> %d", before, got)
	}
}

func TestAnalyzeIsIndependentOfWrite(t *testing.T) {
	// Analyze must be callable without ever writing, which is what a preview
	// pane does, and it must not leave the document in a state Write rejects.
	conv, doc := analyzed(t, defaultOpts())
	if doc.Report() == nil {
		t.Fatal("Analyze produced no report")
	}
	if len(doc.Blocks) == 0 {
		t.Fatal("Analyze produced no blocks")
	}

	// Analyzing again from the same converter must not disturb the first tree.
	src := apiDoc()
	other, err := conv.Analyze(context.Background(), bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("second Analyze: %v", err)
	}
	if len(other.Blocks) != len(doc.Blocks) {
		t.Errorf("two analyses of one file disagree: %d blocks then %d",
			len(doc.Blocks), len(other.Blocks))
	}
	if doc.Blocks[0].ID != other.Blocks[0].ID {
		t.Error("block IDs are not stable across analyses")
	}
}

func TestCancelledContextStops(t *testing.T) {
	// A TUI cancels a conversion when the user navigates away.
	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := apiDoc()
	var out bytes.Buffer
	if _, err := conv.Convert(ctx, bytes.NewReader(src), int64(len(src)), &out); err == nil {
		t.Error("Convert ignored a cancelled context")
	}
}

func TestEveryEPUBEntryIsReachable(t *testing.T) {
	// The manifest and the container must agree: a reader that trusts the
	// manifest should find every file, and no file should be orphaned.
	conv, doc := analyzed(t, defaultOpts())
	var out bytes.Buffer
	if _, err := conv.Write(context.Background(), doc, &out); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	opf := entryContent(t, out.Bytes(), "OEBPS/package.opf")
	checked := 0
	for _, f := range zr.File {
		switch {
		case f.Name == "mimetype",
			f.Name == "META-INF/container.xml",
			f.Name == "OEBPS/package.opf",
			strings.HasSuffix(f.Name, "/"):
			continue
		}
		href := strings.TrimPrefix(f.Name, "OEBPS/")
		checked++
		if !strings.Contains(opf, href) {
			t.Errorf("%s is in the container but not the manifest", f.Name)
		}
	}
	if checked < 3 {
		t.Fatalf("only %d entries checked; the test is not exercising anything", checked)
	}
}
