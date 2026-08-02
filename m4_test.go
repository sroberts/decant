package decant_test

import (
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/hyphen"
	"github.com/sroberts/decant/internal/testpdf"
)

// --- dehyphenation, spec 4.6 ---

func TestHyphenationPatternsLoad(t *testing.T) {
	langs := hyphen.Languages()
	if len(langs) < 8 {
		t.Errorf("only %d languages ship: %v", len(langs), langs)
	}
	for _, l := range langs {
		h, err := hyphen.For(l)
		if err != nil {
			t.Errorf("%s: %v", l, err)
			continue
		}
		if h.PatternCount() < 100 {
			t.Errorf("%s: only %d patterns", l, h.PatternCount())
		}
	}
}

func TestLPPLLanguagesAreNotShipped(t *testing.T) {
	// Spec 4.6 rules out share-alike and renaming conditions and says to drop
	// the language. Russian and Swedish are LPPL-only; THIRD_PARTY.md records
	// the audit. Shipping them would silently change decant's license terms.
	for _, l := range []string{"ru", "sv"} {
		if _, err := hyphen.For(l); err == nil {
			t.Errorf("patterns for %q ship, but that file is LPPL-only", l)
		}
	}
}

func TestLiangMatchesTeX(t *testing.T) {
	// Break points from the standard English patterns, which TeX itself
	// produces for these words.
	h, err := hyphen.For("en")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]int{
		"hyphenation": {2, 6}, // hy-phen-ation
		"typesetting": {4, 7}, // type-set-ting
		"computer":    {3},    // com-puter
		"example":     {2, 4}, // ex-am-ple
		"adipiscing":  {4, 7}, // adip-isc-ing
	}
	for word, want := range cases {
		got := h.BreakPoints(word)
		if len(got) != len(want) {
			t.Errorf("%s: breaks %v, want %v", word, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: breaks %v, want %v", word, got, want)
				break
			}
		}
	}
}

func TestHyphenMinsRespected(t *testing.T) {
	// English hyphenmins are 2 left, 3 right. A break must never strand
	// fewer letters than that.
	h, _ := hyphen.For("en")
	for _, w := range []string{"hyphenation", "typesetting", "everything", "computer"} {
		n := len([]rune(w))
		for _, b := range h.BreakPoints(w) {
			if b < 2 {
				t.Errorf("%s: break at %d leaves fewer than 2 letters on the left", w, b)
			}
			if n-b < 3 {
				t.Errorf("%s: break at %d leaves fewer than 3 letters on the right", w, b)
			}
		}
	}
}

func TestJoinDecisions(t *testing.T) {
	h, _ := hyphen.For("en")
	cases := []struct {
		left, right string
		wantDrop    bool
		why         string
	}{
		// The patterns permit a break, so the hyphen is a line-break artifact.
		{"adip", "iscing", true, "a legal break point"},
		{"type", "setting", true, "a legal break point"},
		// Both fragments capitalized: a hyphenated proper noun.
		{"Sachs", "Wolfe", false, "both capitalized"},
		// A digit beside the hyphen.
		{"COVID", "19", false, "digit on the right"},
		{"section3", "b", false, "digit on the left"},
		// The continuation must start lowercase per spec 4.6.
		{"foo", "Bar", false, "continuation is capitalized"},
	}
	for _, c := range cases {
		d := h.Join(c.left, c.right)
		if d.Drop != c.wantDrop {
			t.Errorf("Join(%q,%q) drop=%v, want %v (%s); reason: %s",
				c.left, c.right, d.Drop, c.wantDrop, c.why, d.Reason)
		}
		if d.Reason == "" {
			t.Errorf("Join(%q,%q) gave no reason", c.left, c.right)
		}
	}
}

// hyphenDoc wraps a word across a line break with a hyphen.
func hyphenDoc() []byte {
	return testpdf.New().
		SetInfo("Title", "Hyphen Document").
		SetInfo("Lang", "en").
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 15, []string{
			"Lorem ipsum dolor sit amet, consectetuer adip-",
			"iscing elit and the sentence continues onward here",
			"to make a paragraph of a believable length.",
		})).
		Build()
}

func TestDehyphenationJoinsWords(t *testing.T) {
	doc := analyze(t, hyphenDoc(), defaultOpts())

	text := blockTexts(doc)
	if !strings.Contains(text, "adipiscing") {
		t.Errorf("the hyphen was not removed:\n%s", text)
	}
	if strings.Contains(text, "adip- iscing") || strings.Contains(text, "adip-iscing") {
		t.Errorf("the fragments were not rejoined:\n%s", text)
	}

	h := doc.Report().Hyphenation
	if h.Dropped < 1 {
		t.Errorf("Hyphenation.Dropped = %d, want at least 1", h.Dropped)
	}
	if h.Language != "en" {
		t.Errorf("Hyphenation.Language = %q, want en", h.Language)
	}
	if len(h.Decisions) == 0 {
		t.Error("no decisions recorded; spec 4.6 requires the reasoning")
	}
}

func TestNoDehyphenateKeepsHyphen(t *testing.T) {
	opts := defaultOpts()
	opts.NoDehyphenate = true

	doc := analyze(t, hyphenDoc(), opts)
	text := blockTexts(doc)

	if !strings.Contains(text, "adip-iscing") {
		t.Errorf("--no-dehyphenate did not preserve the hyphen:\n%s", text)
	}
	if doc.Report().Hyphenation.Dropped != 0 {
		t.Errorf("hyphens were dropped despite --no-dehyphenate")
	}
}

func TestUnsupportedLanguageDisablesDehyphenation(t *testing.T) {
	// Spec 4.6 disables dehyphenation and records it, rather than guessing
	// with English patterns against a language they do not describe.
	src := testpdf.New().
		SetInfo("Title", "Russian Document").
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 15, []string{
			"Some text that will be treated as Russian by the override,",
			"which ships no patterns because the file is LPPL only.",
		})).
		Build()

	opts := defaultOpts()
	opts.Metadata.Language = "ru"

	doc := analyze(t, src, opts)
	if doc.Report().Hyphenation.Language != "" {
		t.Errorf("dehyphenation ran for ru: %q", doc.Report().Hyphenation.Language)
	}

	found := false
	for _, d := range doc.Report().Diagnostics {
		if strings.Contains(d.Message, "no hyphenation patterns ship") {
			found = true
			if d.Severity != decant.SeverityWarning {
				t.Errorf("the notice is severity %q, want warning", d.Severity)
			}
		}
	}
	if !found {
		t.Error("no diagnostic recorded for the missing pattern set")
	}
}

// --- furniture removal, spec 4.5 ---

// runningHeadDoc repeats a head and a folio in the margins of every page.
func runningHeadDoc(pages int) []byte {
	b := testpdf.New().SetInfo("Title", "Running Heads")
	for i := 0; i < pages; i++ {
		head := testpdf.TextPage("F1", 9, 72, 770, 11, []string{"A Book With Running Heads"})
		folio := testpdf.TextPage("F1", 9, 300, 40, 11, []string{itoa(i + 1)})
		body := testpdf.TextPage("F1", 11, 72, 700, 14, []string{
			"Body text on this page which must survive furniture removal",
			"and which runs to more than one line so it reads as prose.",
		})
		b.AddPage(612, 792, head+body+folio)
	}
	return b.Build()
}

func TestFurnitureRemoved(t *testing.T) {
	doc := analyze(t, runningHeadDoc(10), defaultOpts())

	text := blockTexts(doc)
	if strings.Contains(text, "A Book With Running Heads") {
		t.Errorf("the running head survived:\n%s", text)
	}
	if !strings.Contains(text, "Body text on this page") {
		t.Errorf("furniture removal took the body text:\n%s", text)
	}
	if doc.Report().FurnitureRemoved == 0 {
		t.Error("FurnitureRemoved is 0")
	}
}

func TestKeepHeadersRetainsFurniture(t *testing.T) {
	opts := defaultOpts()
	opts.KeepHeaders = true

	doc := analyze(t, runningHeadDoc(10), opts)
	if !strings.Contains(blockTexts(doc), "A Book With Running Heads") {
		t.Error("--keep-headers did not retain the running head")
	}
	if doc.Report().FurnitureRemoved != 0 {
		t.Error("furniture was removed despite --keep-headers")
	}
}

func TestShortDocumentSkipsFurnitureRemoval(t *testing.T) {
	// Spec 4.5 skips removal below 5 pages, where a repeated line is more
	// likely to be content than a running head.
	doc := analyze(t, runningHeadDoc(3), defaultOpts())
	if doc.Report().FurnitureRemoved != 0 {
		t.Errorf("removed furniture from a 3-page document: %d",
			doc.Report().FurnitureRemoved)
	}
	if !strings.Contains(blockTexts(doc), "A Book With Running Heads") {
		t.Error("the head was removed from a short document")
	}
}

// --- lists, spec 4.6 ---

func listDoc() []byte {
	body := testpdf.TextPage("F1", 11, 72, 740, 14, []string{
		"An introduction paragraph before the list which runs to",
		"more than one line of ordinary body text here.",
	})
	list := testpdf.ListPage("F1", 11, 72, 690, 14,
		[]string{"1.", "2.", "3."},
		[][]string{
			{"The first item of the list", "continuing onto a second line"},
			{"The second item of the list", "also continuing onward"},
			{"The third item of the list", "and its continuation line"},
		})
	return testpdf.New().
		SetInfo("Title", "List Document").
		AddPage(612, 792, body+list).
		Build()
}

func TestOrderedListDetected(t *testing.T) {
	data, _ := buildDoc(t, listDoc(), defaultOpts())
	text := allChapterText(t, data)

	if !strings.Contains(text, "<ol>") {
		t.Errorf("no ordered list in the output:\n%s", text)
	}
	if n := strings.Count(text, "<li>"); n != 3 {
		t.Errorf("found %d list items, want 3:\n%s", n, text)
	}
	// The marker itself must not survive into the item text.
	if strings.Contains(text, "<li>1.") {
		t.Errorf("the marker was not stripped:\n%s", text)
	}
}

func TestBulletListDetected(t *testing.T) {
	body := testpdf.TextPage("F1", 11, 72, 740, 14, []string{
		"An introduction paragraph before the list which runs to",
		"more than one line of ordinary body text here.",
	})
	// An asterisk rather than a bullet glyph: the fixture writes single-byte
	// codes into a base-14 font, where U+2022 has no encoding. Both are
	// bullet markers as far as spec 4.6 is concerned.
	list := testpdf.ListPage("F1", 11, 72, 690, 14,
		[]string{"*", "*", "*"},
		[][]string{
			{"The first bullet item", "continuing onto a second line"},
			{"The second bullet item", "also continuing onward"},
			{"The third bullet item", "and its continuation line"},
		})
	src := testpdf.New().AddPage(612, 792, body+list).Build()

	data, _ := buildDoc(t, src, defaultOpts())
	text := allChapterText(t, data)
	if !strings.Contains(text, "<ul>") {
		t.Errorf("no unordered list in the output:\n%s", text)
	}
}

func TestListStartInferred(t *testing.T) {
	// Spec 4.6 infers the start attribute from the first marker.
	body := testpdf.TextPage("F1", 11, 72, 740, 14, []string{
		"An introduction paragraph before the list which runs to",
		"more than one line of ordinary body text here.",
	})
	list := testpdf.ListPage("F1", 11, 72, 690, 14,
		[]string{"7.", "8.", "9."},
		[][]string{
			{"The seventh item here", "continuing onto a second line"},
			{"The eighth item here", "also continuing onward"},
			{"The ninth item here", "and its continuation line"},
		})
	src := testpdf.New().AddPage(612, 792, body+list).Build()

	data, _ := buildDoc(t, src, defaultOpts())
	if text := allChapterText(t, data); !strings.Contains(text, `<ol start="7">`) {
		t.Errorf("the list start was not inferred:\n%s", text)
	}
}

func TestLoneMarkedLineIsNotAList(t *testing.T) {
	// "0. Auflage" on a title page is a byline, not a one-item list. Spec 4.6
	// requires a hanging indent, which a single line cannot show.
	src := testpdf.New().
		AddPage(612, 792, testpdf.TextPage("F1", 11, 72, 700, 14, []string{
			"0. Auflage, 31. Dezember 2016 Martin Thoma",
		})+testpdf.TextPage("F1", 11, 72, 600, 14, []string{
			"Ordinary body text following the byline which runs to more",
			"than one line so the document has real prose in it.",
		})).
		Build()

	data, _ := buildDoc(t, src, defaultOpts())
	if text := allChapterText(t, data); strings.Contains(text, "<ol") {
		t.Errorf("a lone marked line became a list:\n%s", text)
	}
}

// --- footnotes, spec 4.6 ---

func footnoteDoc() []byte {
	body := testpdf.SuperscriptLine("F1", 11, 72, 700,
		"Body text with a reference marker ", "1", " and then more text after it.")
	more := testpdf.TextPage("F1", 11, 72, 680, 14, []string{
		"A second line of body text so the block reads as a paragraph",
		"and the document has enough prose to establish a body font.",
	})
	// The note sits in the bottom 20% band, set smaller than the body.
	note := testpdf.TextPage("F1", 8, 72, 90, 10, []string{
		"1 A footnote explaining the reference marker above it.",
	})
	return testpdf.New().
		SetInfo("Title", "Footnote Document").
		AddPage(612, 792, body+more+note).
		Build()
}

func TestSuperscriptDetected(t *testing.T) {
	data, _ := buildDoc(t, footnoteDoc(), defaultOpts())
	text := allChapterText(t, data)

	if !strings.Contains(text, "<sup>") {
		t.Errorf("no superscript in the output:\n%s", text)
	}
}

func TestFootnoteDetectedAndLinked(t *testing.T) {
	data, _ := buildDoc(t, footnoteDoc(), defaultOpts())
	text := allChapterText(t, data)

	if !strings.Contains(text, `epub:type="footnote"`) {
		t.Errorf("the note was not marked as a footnote:\n%s", text)
	}
	if !strings.Contains(text, `epub:type="noteref"`) {
		t.Errorf("the marker was not linked to the note:\n%s", text)
	}

	// The noteref must point at the footnote's own id.
	i := strings.Index(text, `epub:type="noteref" href="#`)
	if i < 0 {
		t.Fatalf("no noteref href:\n%s", text)
	}
	rest := text[i+len(`epub:type="noteref" href="#`):]
	id := rest[:strings.Index(rest, `"`)]
	if !strings.Contains(text, `epub:type="footnote" id="`+id+`"`) {
		t.Errorf("noteref points at %q, which is not a footnote id:\n%s", id, text)
	}
}

func TestSuperscriptMarksDoNotLeak(t *testing.T) {
	// Superscript runs are bracketed with private-use code points during
	// layout. None may reach the output or the block text.
	data, _ := buildDoc(t, footnoteDoc(), defaultOpts())
	text := allChapterText(t, data)
	if strings.ContainsAny(text, "") {
		t.Error("superscript sentinels leaked into the XHTML")
	}

	doc := analyze(t, footnoteDoc(), defaultOpts())
	for _, b := range doc.Blocks {
		if strings.ContainsAny(b.ID, "") {
			t.Errorf("a block id carries a sentinel: %q", b.ID)
		}
	}
}

// --- code blocks, spec 4.6 ---

func TestFixedPitchBecomesCode(t *testing.T) {
	body := testpdf.TextPage("F1", 11, 72, 720, 14, []string{
		"An introduction paragraph in the body font which runs to more",
		"than one line so it establishes the document body font clearly.",
		"And a third line to be certain of the weighted mode.",
	})
	// F4 is Courier, a fixed-pitch family.
	code := testpdf.TextPage("F4", 10, 72, 640, 12, []string{
		"func main() {",
		"    println(\\\"hello\\\")",
		"}",
	})
	src := testpdf.New().AddPage(612, 792, body+code).Build()

	data, _ := buildDoc(t, src, defaultOpts())
	text := allChapterText(t, data)
	if !strings.Contains(text, "<pre><code>") {
		t.Errorf("the monospaced block was not made code:\n%s", text)
	}
}

// --- integration ---

func TestM4OutputIsDeterministic(t *testing.T) {
	for _, src := range [][]byte{hyphenDoc(), listDoc(), footnoteDoc(), runningHeadDoc(8)} {
		a, _ := buildDoc(t, src, defaultOpts())
		b, _ := buildDoc(t, src, defaultOpts())
		if !bytesEqual(a, b) {
			t.Error("two conversions produced different bytes")
		}
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- dropped vector artwork ---

// vectorDoc draws a diagram with path operators alongside real body text.
func vectorDoc(paths int) []byte {
	body := testpdf.TextPage("F1", 11, 72, 720, 14, []string{
		"Body text above the diagram which runs to more than one line",
		"so the page converts normally and is not taken for a scan.",
	})
	return testpdf.New().
		SetInfo("Title", "Vector Document").
		AddPage(612, 792, body+testpdf.VectorPaths(paths, 100, 300, 40)).
		Build()
}

func TestDroppedVectorArtworkIsReported(t *testing.T) {
	// Spec section 1 puts vector conversion out of scope for v1 and section 13
	// keeps rasterization open, so a chart drawn as paths is lost. Principle 3
	// requires that to be visible rather than silent.
	doc := analyze(t, vectorDoc(40), defaultOpts())
	rep := doc.Report()

	if rep.VectorPagesDropped != 1 {
		t.Errorf("VectorPagesDropped = %d, want 1", rep.VectorPagesDropped)
	}
	if rep.VectorPaintsDropped < 40 {
		t.Errorf("VectorPaintsDropped = %d, want at least 40", rep.VectorPaintsDropped)
	}

	found := false
	for _, d := range rep.Diagnostics {
		if strings.Contains(d.Message, "vector artwork that was not rendered") {
			found = true
			if d.Severity != decant.SeverityWarning {
				t.Errorf("severity is %q, want warning: losing a chart is not routine",
					d.Severity)
			}
		}
	}
	if !found {
		t.Error("no diagnostic recorded for the dropped artwork")
	}

	// The text must be unaffected.
	if !strings.Contains(blockTexts(doc), "Body text above the diagram") {
		t.Error("the body text was damaged")
	}
}

func TestIncidentalPathsAreNotReported(t *testing.T) {
	// Rules, underlines, and table borders are paths too. Reporting them as
	// lost artwork would be noise a reader cannot act on.
	doc := analyze(t, vectorDoc(3), defaultOpts())
	if n := doc.Report().VectorPagesDropped; n != 0 {
		t.Errorf("VectorPagesDropped = %d for a page with three rules, want 0", n)
	}
}

func TestClipPathsAreNotArtwork(t *testing.T) {
	// "W n" sets a clip and paints nothing. Counting it would report every
	// clipped region, which is most pages that place an image.
	body := testpdf.TextPage("F1", 11, 72, 720, 14, []string{
		"Body text on a page that clips heavily but paints no artwork,",
		"running to more than one line so it converts normally.",
	})
	src := testpdf.New().
		AddPage(612, 792, body+testpdf.ClipPath(40, 100, 300, 200, 200)).
		Build()

	doc := analyze(t, src, defaultOpts())
	if n := doc.Report().VectorPagesDropped; n != 0 {
		t.Errorf("VectorPagesDropped = %d; the W n idiom paints nothing", n)
	}
}

func TestVectorThresholdIsTunable(t *testing.T) {
	opts := defaultOpts()
	opts.Heuristics.VectorMinPaints = 2

	doc := analyze(t, vectorDoc(3), opts)
	if doc.Report().VectorPagesDropped != 1 {
		t.Error("lowering VectorMinPaints did not lower the reporting threshold")
	}
}
