package decant_test

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// headingDoc is a document with three sections, each opened by a heading set
// noticeably larger than the body.
func headingDoc() []byte {
	return testpdf.New().
		SetInfo("Title", "Structured Document").
		AddPage(612, 792, testpdf.HeadingPage("F1", 10, 18, 13, [][]string{
			{
				"The First Section",
				"Body text under the first heading runs across a couple of",
				"lines so the block has real substance to classify.",
			},
			{
				"The Second Section",
				"More body text under the second heading, also spanning",
				"more than a single line of set text.",
			},
			{
				"The Third Section",
				"A final passage of body text closing out the document",
				"with two lines of its own.",
			},
		})).
		Build()
}

func analyze(t *testing.T, src []byte, opts decant.Options) *decant.Document {
	t.Helper()
	conv, err := decant.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc, err := conv.Analyze(context.Background(), bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return doc
}

func headings(doc *decant.Document) []decant.Block {
	var out []decant.Block
	for _, b := range doc.Blocks {
		if b.Kind == decant.KindHeading {
			out = append(out, b)
		}
	}
	return out
}

func TestHeadingsDetectedBySize(t *testing.T) {
	doc := analyze(t, headingDoc(), defaultOpts())

	hs := headings(doc)
	if len(hs) != 3 {
		t.Fatalf("detected %d headings, want 3:\n%s", len(hs), dumpBlocks(doc))
	}
	for i, want := range []string{"The First Section", "The Second Section", "The Third Section"} {
		if hs[i].Text != want {
			t.Errorf("heading %d = %q, want %q", i, hs[i].Text, want)
		}
		if hs[i].Level != 1 {
			t.Errorf("heading %d level = %d, want 1 (only one heading size)", i, hs[i].Level)
		}
	}
}

func TestBodyTextNotClassifiedAsHeading(t *testing.T) {
	doc := analyze(t, headingDoc(), defaultOpts())
	for _, b := range doc.Blocks {
		if b.Kind == decant.KindHeading && strings.HasPrefix(b.Text, "Body text") {
			t.Errorf("body text was classified as a heading: %q", b.Text)
		}
	}
	if n := len(doc.Blocks) - len(headings(doc)); n < 3 {
		t.Errorf("only %d paragraphs survived classification", n)
	}
}

func TestBodyFontIsDocumentWide(t *testing.T) {
	// A page that is mostly display type must not shift the body font: the
	// mode is computed across the whole document, per spec 4.6.
	displayPage := testpdf.TextPage("F1", 24, 72, 720, 30, []string{
		"A Title Page",
		"With Large Type",
	})
	bodyPage := testpdf.TextPage("F1", 10, 72, 720, 13, []string{
		"Ordinary body text at ten point running along the measure here,",
		"continuing for several lines so it dominates the glyph count,",
		"which is what the weighted mode is computed from in the end.",
		"More body text to be certain the ten point size wins outright.",
	})

	src := testpdf.New().
		AddPage(612, 792, displayPage).
		AddPage(612, 792, bodyPage).
		Build()

	doc := analyze(t, src, defaultOpts())
	if got := doc.Report().BodyFont; !strings.Contains(got, "10.0pt") {
		t.Errorf("body font = %q, want 10pt", got)
	}

	// The title page's large type should therefore be headings.
	found := false
	for _, b := range doc.Blocks {
		if b.Page == 0 && b.Kind == decant.KindHeading {
			found = true
		}
	}
	if !found {
		t.Errorf("the display-type page produced no headings:\n%s", dumpBlocks(doc))
	}
}

func TestHeadingLevelsRankBySize(t *testing.T) {
	// Three distinct heading sizes must map to h1, h2, h3 in size order.
	content := testpdf.HeadingPageAt("F1", 10, 20, 13, 1300, [][]string{{
		"Top Level Heading",
		"Body text under the top level heading spanning a couple of lines",
		"so the paragraph is unambiguous to the classifier.",
	}}) + testpdf.HeadingPageAt("F1", 10, 15, 13, 1150, [][]string{{
		"Middle Level Heading",
		"Body text under the middle level heading, also more than one",
		"line long so it reads as a paragraph.",
	}})

	src := testpdf.New().AddPage(612, 1400, content).Build()
	doc := analyze(t, src, defaultOpts())

	byText := map[string]int{}
	for _, b := range headings(doc) {
		byText[b.Text] = b.Level
	}
	top, okTop := byText["Top Level Heading"]
	mid, okMid := byText["Middle Level Heading"]
	if !okTop || !okMid {
		t.Fatalf("expected both headings, got %v\n%s", byText, dumpBlocks(doc))
	}
	if top >= mid {
		t.Errorf("level ranking is wrong: 20pt heading is h%d, 15pt heading is h%d", top, mid)
	}
}

func TestBoldShortLineIsHeading(t *testing.T) {
	// Same size as the body, but bold, short, and no terminal punctuation.
	body := testpdf.TextPage("F1", 10, 72, 720, 13, []string{
		"Ordinary body text that establishes the document body font by",
		"carrying the majority of the glyphs on this page and the next,",
		"running on for several lines to be certain of the weighted mode.",
	})
	boldHead := "BT /F3 10 Tf 1 0 0 1 72 640 Tm (A Bold Run In Heading) Tj ET\n"
	more := testpdf.TextPage("F1", 10, 72, 620, 13, []string{
		"Further body text following the bold line above, again running",
		"to several lines so the body font keeps its majority.",
	})

	src := testpdf.New().AddPage(612, 792, body+boldHead+more).Build()
	doc := analyze(t, src, defaultOpts())

	found := false
	for _, b := range headings(doc) {
		if strings.Contains(b.Text, "A Bold Run In Heading") {
			found = true
		}
	}
	if !found {
		t.Errorf("a bold, short, unpunctuated line was not made a heading:\n%s", dumpBlocks(doc))
	}
}

func TestLongPassageAtLargerSizeIsNotHeading(t *testing.T) {
	// Spec 4.6 makes size alone sufficient. HeadingMaxWords guards against a
	// long epigraph set slightly large becoming a heading and splitting the
	// book at it.
	body := testpdf.TextPage("F1", 10, 72, 720, 13, []string{
		"Body text at ten point that establishes the document body font",
		"and carries most of the glyphs across the whole of this page,",
		"continuing for enough lines to dominate the weighted mode.",
		"Still more body text at ten point to settle the matter firmly.",
		"And another line of ordinary body text at the same ten points.",
	})
	// A long passage at 12pt, above the 15% threshold.
	long := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		long = append(long,
			"A long epigraph line set slightly larger than the body text is")
	}
	epigraph := testpdf.TextPage("F1", 12, 72, 600, 15, long)

	src := testpdf.New().AddPage(612, 1000, body+epigraph).Build()
	doc := analyze(t, src, defaultOpts())

	for _, b := range headings(doc) {
		if strings.HasPrefix(b.Text, "A long epigraph") {
			t.Errorf("a %d word passage became a heading:\n%q",
				len(strings.Fields(b.Text)), b.Text)
		}
	}
}

func TestChapterSplittingAtHeadings(t *testing.T) {
	data, rep := buildDoc(t, headingDoc(), defaultOpts())

	if rep.Chapters != 3 {
		t.Errorf("got %d chapters, want 3 (one per h1)", rep.Chapters)
	}
	names := entryNames(t, data)
	for _, want := range []string{
		"OEBPS/text/ch001.xhtml",
		"OEBPS/text/ch002.xhtml",
		"OEBPS/text/ch003.xhtml",
	} {
		if !names[want] {
			t.Errorf("missing %s", want)
		}
	}

	// Each chapter opens with its heading.
	ch2 := entryContent(t, data, "OEBPS/text/ch002.xhtml")
	if !strings.Contains(ch2, "The Second Section") {
		t.Errorf("chapter 2 does not open with its heading:\n%s", ch2)
	}
	if strings.Contains(ch2, "The First Section") {
		t.Error("chapter 2 contains chapter 1's heading")
	}
}

func TestHeadingsRenderAsHeadingElements(t *testing.T) {
	data, _ := buildDoc(t, headingDoc(), defaultOpts())
	text := allChapterText(t, data)

	if !strings.Contains(text, "<h1") {
		t.Errorf("no h1 element in the output:\n%s", text)
	}
	re := regexp.MustCompile(`<h1 id="[^"]+">The First Section</h1>`)
	if !re.MatchString(text) {
		t.Errorf("heading is not rendered with an anchor id:\n%s", text)
	}
}

func TestNavReflectsHeadings(t *testing.T) {
	data, _ := buildDoc(t, headingDoc(), defaultOpts())

	nav := entryContent(t, data, "OEBPS/nav.xhtml")
	for _, want := range []string{"The First Section", "The Second Section", "The Third Section"} {
		if !strings.Contains(nav, want) {
			t.Errorf("nav.xhtml is missing %q:\n%s", want, nav)
		}
	}

	ncx := entryContent(t, data, "OEBPS/toc.ncx")
	if strings.Count(ncx, "<navPoint") != 3 {
		t.Errorf("toc.ncx has %d navPoints, want 3:\n%s",
			strings.Count(ncx, "<navPoint"), ncx)
	}
}

func TestNavNestsBySubheading(t *testing.T) {
	// An h1 followed by two h2s must nest, not flatten.
	content := testpdf.HeadingPageAt("F1", 10, 22, 13, 1500, [][]string{{
		"Part One",
		"Body text introducing the part, running to a couple of lines so",
		"the classifier sees a genuine paragraph beneath the heading.",
	}}) + testpdf.HeadingPageAt("F1", 10, 15, 13, 1330, [][]string{
		{
			"Chapter A",
			"Body text of chapter A running to more than one line so it",
			"reads clearly as body rather than display type.",
		},
		{
			"Chapter B",
			"Body text of chapter B, likewise more than a single line in",
			"length so the block is unambiguous.",
		},
	})

	src := testpdf.New().AddPage(612, 1600, content).Build()
	opts := defaultOpts()
	opts.SplitAt = decant.SplitAtNone

	data, _ := buildDoc(t, src, opts)
	nav := entryContent(t, data, "OEBPS/nav.xhtml")

	// The nested list must appear inside the Part One list item.
	partIdx := strings.Index(nav, "Part One")
	chapIdx := strings.Index(nav, "Chapter A")
	if partIdx < 0 || chapIdx < 0 {
		t.Fatalf("nav is missing entries:\n%s", nav)
	}
	between := nav[partIdx:chapIdx]
	if !strings.Contains(between, "<ol>") {
		t.Errorf("Chapter A is not nested under Part One:\n%s", nav)
	}
}

func TestOutlineOverridesInferredStructure(t *testing.T) {
	// The PDF outline is authoritative: its titles and depths win over
	// anything inferred from type size.
	src := testpdf.New().
		SetInfo("Title", "Outlined Document").
		AddPage(612, 792, testpdf.HeadingPage("F1", 10, 16, 13, [][]string{{
			"Inferred Heading Text",
			"Body text under the heading spanning a couple of lines so the",
			"block is a genuine paragraph for the classifier.",
		}})).
		AddPage(612, 792, testpdf.HeadingPage("F1", 10, 16, 13, [][]string{{
			"Another Inferred Heading",
			"More body text on the second page, also more than one line",
			"long so it classifies cleanly as a paragraph.",
		}})).
		AddBookmark("Authoritative Title", 0, 720).
		AddBookmark("Second Authoritative Title", 1, 720).
		Build()

	doc := analyze(t, src, defaultOpts())

	texts := map[string]bool{}
	for _, b := range headings(doc) {
		texts[b.Text] = true
	}
	if !texts["Authoritative Title"] {
		t.Errorf("the outline title did not replace the inferred text:\n%s", dumpBlocks(doc))
	}
	if texts["Inferred Heading Text"] {
		t.Error("the inferred heading text survived alongside the outline title")
	}
}

func TestOutlineDepthSetsHeadingLevel(t *testing.T) {
	// A nested bookmark must force its block to the outline's depth.
	src := testpdf.New().
		AddPage(612, 792, testpdf.HeadingPage("F1", 10, 16, 13, [][]string{{
			"Section One",
			"Body text for section one spanning a couple of lines so the",
			"paragraph is unambiguous.",
		}})).
		AddPage(612, 792, testpdf.HeadingPage("F1", 10, 16, 13, [][]string{{
			"Subsection",
			"Body text for the subsection, likewise more than one line so",
			"it reads as body text.",
		}})).
		AddNestedBookmark("Top", 0, 720, "Nested", 1, 720).
		Build()

	doc := analyze(t, src, defaultOpts())

	levels := map[string]int{}
	for _, b := range headings(doc) {
		levels[b.Text] = b.Level
	}
	if levels["Top"] != 1 {
		t.Errorf("top-level bookmark produced level %d, want 1 (%v)", levels["Top"], levels)
	}
	if levels["Nested"] != 2 {
		t.Errorf("nested bookmark produced level %d, want 2 (%v)", levels["Nested"], levels)
	}
}

func TestTwoColumnReadingOrder(t *testing.T) {
	// The core M2 case: an academic two-column page must read down the left
	// column then down the right, not across.
	// A real two-column page carries many rows; the ColumnMinRows guard
	// deliberately refuses to trust a projection profile with fewer.
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
	src := testpdf.New().
		AddPage(612, 792, testpdf.TwoColumnPage("F1", 10, 13, "", left, right)).
		Build()

	doc := analyze(t, src, defaultOpts())
	if doc.Report().MultiColumnPages != 1 {
		t.Fatalf("column detection did not fire:\n%s", dumpBlocks(doc))
	}

	var sb strings.Builder
	for _, b := range doc.Blocks {
		sb.WriteString(b.Text)
		sb.WriteString(" ")
	}
	all := sb.String()

	lastAlpha := strings.LastIndex(all, "Alpha six")
	firstBeta := strings.Index(all, "Beta one")
	if lastAlpha < 0 || firstBeta < 0 {
		t.Fatalf("column text missing:\n%s", all)
	}
	if lastAlpha > firstBeta {
		t.Errorf("columns interleaved; reading order is across not down:\n%s", all)
	}
}

func TestTwoColumnWithSpanningHeading(t *testing.T) {
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
	src := testpdf.New().
		AddPage(612, 792, testpdf.TwoColumnPage("F1", 10, 13,
			"A Paper Title That Spans The Full Measure Of Both Columns", left, right)).
		Build()

	doc := analyze(t, src, defaultOpts())
	if len(doc.Blocks) == 0 {
		t.Fatal("no blocks")
	}
	if !strings.HasPrefix(doc.Blocks[0].Text, "A Paper Title") {
		t.Errorf("first block is %q, want the spanning title:\n%s",
			doc.Blocks[0].Text, dumpBlocks(doc))
	}
	if !strings.HasSuffix(doc.Blocks[0].Text, "Both Columns") {
		t.Errorf("the spanning title was truncated at the gutter: %q", doc.Blocks[0].Text)
	}
}

func TestForcedColumnsOption(t *testing.T) {
	left := []string{
		"Alpha one here", "Alpha two here", "Alpha three now",
		"Alpha four here", "Alpha five here", "Alpha six now",
		"Alpha seven here", "Alpha eight here", "Alpha nine now",
		"Alpha ten here",
	}
	right := []string{
		"Beta one here", "Beta two here", "Beta three now",
		"Beta four here", "Beta five here", "Beta six now",
		"Beta seven here", "Beta eight here", "Beta nine now",
		"Beta ten here",
	}
	src := testpdf.New().
		AddPage(612, 792, testpdf.TwoColumnPage("F1", 10, 13, "", left, right)).
		Build()

	opts := defaultOpts()
	opts.Columns = 1
	doc := analyze(t, src, opts)

	if doc.Report().MultiColumnPages != 0 {
		t.Error("--columns=1 did not suppress column detection")
	}
}

func TestColumnsOptionValidated(t *testing.T) {
	opts := decant.DefaultOptions()
	opts.Columns = 7
	if _, err := decant.New(opts); err == nil {
		t.Error("New accepted an out-of-range column count")
	}
}

func TestSingleColumnUnaffectedByColumnDetection(t *testing.T) {
	// The regression that matters: ordinary prose must not be split into
	// phantom columns.
	doc := analyze(t, simpleDoc(), defaultOpts())
	if n := doc.Report().MultiColumnPages; n != 0 {
		t.Errorf("column detection fired on %d single-column pages", n)
	}
	if !strings.Contains(blockTexts(doc), "The first paragraph begins here") {
		t.Errorf("single-column text was damaged:\n%s", dumpBlocks(doc))
	}
}

func TestStructureFingerprintIsStable(t *testing.T) {
	// Spec section 10 asserts on a structure fingerprint rather than exact
	// XHTML, so formatting refactors do not churn the corpus.
	want := fingerprint(analyze(t, headingDoc(), defaultOpts()))
	got := fingerprint(analyze(t, headingDoc(), defaultOpts()))
	if want != got {
		t.Errorf("fingerprint changed between runs:\n%s\n%s", want, got)
	}
	if !strings.HasPrefix(want, "h1 p h1 p h1 p") {
		t.Errorf("fingerprint = %q, want alternating headings and paragraphs", want)
	}
}

// fingerprint renders the document's structure as an ordered list of element
// types and heading levels.
func fingerprint(doc *decant.Document) string {
	var sb strings.Builder
	for i, b := range doc.Blocks {
		if i > 0 {
			sb.WriteByte(' ')
		}
		switch b.Kind {
		case decant.KindHeading:
			sb.WriteString("h")
			sb.WriteByte(byte('0' + b.Level))
		case decant.KindParagraph:
			sb.WriteString("p")
		default:
			sb.WriteString(string(b.Kind))
		}
	}
	return sb.String()
}

func dumpBlocks(doc *decant.Document) string {
	var sb strings.Builder
	for i, b := range doc.Blocks {
		sb.WriteString(strings.TrimSpace(
			"  [" + itoa(i) + "] p" + itoa(b.Page) + " " + string(b.Kind) +
				" lv" + itoa(b.Level) + " " + fmtSize(b.Size) + " " + b.Font + ": " + b.Text))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func blockTexts(doc *decant.Document) string {
	var sb strings.Builder
	for _, b := range doc.Blocks {
		sb.WriteString(b.Text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func fmtSize(v float64) string {
	return itoa(int(v*10)/10) + "." + itoa(int(v*10)%10) + "pt"
}
