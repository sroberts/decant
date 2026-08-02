package decant_test

import (
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// --- table detection, spec 4.8 ---

// tableDoc builds a page carrying a ruled table below a paragraph of body
// text, so the document has enough prose for the body font to be identified.
func tableDoc(rows [][]testpdf.TableCell) []byte {
	body := testpdf.TextPage("F1", 11, 72, 720, 14, []string{
		"An introductory paragraph sitting above the table, long enough that",
		"the body font is identified from prose rather than from cell text.",
	})
	tbl := testpdf.RuledTable("F1", 11, 72, 660, 90, 20, rows)
	return testpdf.New().AddPage(612, 792, body+tbl).Build()
}

// chapterXHTML converts a document and returns every chapter's markup.
func chapterXHTML(t *testing.T, src []byte, opts decant.Options) string {
	t.Helper()
	out, _ := buildDoc(t, src, opts)
	return allChapterText(t, out)
}

func tableBlocks(doc *decant.Document) []decant.Block {
	var out []decant.Block
	for _, b := range doc.Blocks {
		if b.Kind == decant.KindTable {
			out = append(out, b)
		}
	}
	return out
}

func cells(rows ...[]string) [][]testpdf.TableCell {
	out := make([][]testpdf.TableCell, len(rows))
	for i, r := range rows {
		row := make([]testpdf.TableCell, len(r))
		for j, s := range r {
			row[j] = testpdf.TableCell{Text: s}
		}
		out[i] = row
	}
	return out
}

func TestRuledTableDetected(t *testing.T) {
	doc := analyze(t, tableDoc(cells(
		[]string{"Region", "Revenue", "Growth"},
		[]string{"Northeast", "412000", "12 pct"},
		[]string{"Midwest", "298000", "4 pct"},
		[]string{"Pacific", "530000", "19 pct"},
	)), defaultOpts())

	tables := tableBlocks(doc)
	if len(tables) != 1 {
		t.Fatalf("%d tables detected, want 1", len(tables))
	}
	if n := len(tables[0].TableRows); n != 4 {
		t.Errorf("%d rows, want 4", n)
	}
	// A full grid with corroborating alignment is the high-confidence case.
	if got := doc.Report().Tables["high"]; got != 1 {
		t.Errorf("Tables[high] = %d, want 1; report says %v", got, doc.Report().Tables)
	}
}

func TestCellsAreAssembledFromGlyphs(t *testing.T) {
	// A table row shares one baseline, so line assembly merges the whole row
	// into a single fragment. Spec 4.8 assigns text to cells by glyph
	// position; taking whole lines puts every value in the first cell.
	doc := analyze(t, tableDoc(cells(
		[]string{"Region", "Revenue", "Growth"},
		[]string{"Northeast", "412000", "12 pct"},
		[]string{"Midwest", "298000", "4 pct"},
	)), defaultOpts())

	tables := tableBlocks(doc)
	if len(tables) != 1 {
		t.Fatalf("%d tables detected, want 1", len(tables))
	}
	row := tables[0].TableRows[1]
	if len(row) != 3 {
		t.Fatalf("row has %d cells, want 3: %#v", len(row), row)
	}
	want := []string{"Northeast", "412000", "12 pct"}
	for i, w := range want {
		if got := strings.TrimSpace(row[i].Text); got != w {
			t.Errorf("cell %d = %q, want %q", i, got, w)
		}
	}
}

func TestColSpanFromMissingVerticalRule(t *testing.T) {
	doc := analyze(t, tableDoc([][]testpdf.TableCell{
		{{Text: "Quarterly results", Span: 3}},
		{{Text: "Region"}, {Text: "Revenue"}, {Text: "Growth"}},
		{{Text: "Northeast"}, {Text: "412000"}, {Text: "12 pct"}},
		{{Text: "Midwest"}, {Text: "298000"}, {Text: "4 pct"}},
	}), defaultOpts())

	tables := tableBlocks(doc)
	if len(tables) != 1 {
		t.Fatalf("%d tables detected, want 1", len(tables))
	}
	first := tables[0].TableRows[0]
	if len(first) != 1 {
		t.Fatalf("first row has %d cells, want 1 spanning cell: %#v", len(first), first)
	}
	if first[0].ColSpan != 3 {
		t.Errorf("ColSpan = %d, want 3", first[0].ColSpan)
	}
}

func TestTableRendersAsTableElement(t *testing.T) {
	opts := defaultOpts()
	opts.Tables = decant.TableHTML
	xhtml := chapterXHTML(t, tableDoc(cells(
		[]string{"Region", "Revenue"},
		[]string{"Northeast", "412000"},
		[]string{"Midwest", "298000"},
	)), opts)

	if !strings.Contains(xhtml, "<table") {
		t.Fatalf("no table element emitted:\n%s", xhtml)
	}
	// Spec 4.8 marks the first row as headers.
	if !strings.Contains(xhtml, "<th>Region</th>") {
		t.Errorf("first row is not a header row:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, "<td>412000</td>") {
		t.Errorf("body cell missing:\n%s", xhtml)
	}
}

func TestTableTextModeUsesPre(t *testing.T) {
	opts := defaultOpts()
	opts.Tables = decant.TableText
	xhtml := chapterXHTML(t, tableDoc(cells(
		[]string{"Region", "Revenue"},
		[]string{"Northeast", "412000"},
		[]string{"Midwest", "298000"},
	)), opts)

	if strings.Contains(xhtml, "<table") {
		t.Errorf("text mode emitted a table element:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, "<pre") {
		t.Errorf("text mode did not emit a pre element:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, "Northeast") {
		t.Errorf("cell text was lost:\n%s", xhtml)
	}
}

func TestTableDropModeKeepsText(t *testing.T) {
	// Spec 4.8: dropping a table must not drop its contents.
	opts := defaultOpts()
	opts.Tables = decant.TableDrop
	doc := analyze(t, tableDoc(cells(
		[]string{"Region", "Revenue"},
		[]string{"Northeast", "412000"},
		[]string{"Midwest", "298000"},
	)), opts)

	if n := len(tableBlocks(doc)); n != 0 {
		t.Errorf("%d table blocks survived drop mode", n)
	}
	if !strings.Contains(blockTexts(doc), "Northeast") {
		t.Errorf("drop mode discarded the table's text:\n%s", blockTexts(doc))
	}
}

func TestCrossPointProfileForcesTextTables(t *testing.T) {
	// Spec 5.1: the reader has no table layout, so a table element renders as
	// a run-on paragraph. Text mode at least preserves the columns.
	opts := decant.DefaultOptions()
	opts.Profile = decant.ProfileCrossPoint
	opts.ApplyProfileDefaults()
	if opts.Tables != decant.TableText {
		t.Errorf("crosspoint table mode is %q, want %q", opts.Tables, decant.TableText)
	}
}

func TestSingleCharacterGridIsNotATable(t *testing.T) {
	// A plotted graph's axes and a diagram drawn from repeated marks both form
	// a filled grid. Marking either up as a table shreds a figure into cells.
	doc := analyze(t, tableDoc(cells(
		[]string{"*", "*", "*"},
		[]string{"*", "*", "*"},
		[]string{"*", "*", "*"},
	)), defaultOpts())

	if n := len(tableBlocks(doc)); n != 0 {
		t.Errorf("%d tables detected from a grid of single glyphs", n)
	}
}

func TestSparseGridIsNotATable(t *testing.T) {
	// A grid whose cells are mostly empty is a layout frame, not a table.
	doc := analyze(t, tableDoc([][]testpdf.TableCell{
		{{Text: "Region"}, {Text: ""}, {Text: ""}},
		{{Text: ""}, {Text: ""}, {Text: ""}},
		{{Text: ""}, {Text: ""}, {Text: "412000"}},
	}), defaultOpts())

	if n := len(tableBlocks(doc)); n != 0 {
		t.Errorf("%d tables detected from a mostly empty grid", n)
	}
}

func TestProseIsNotATable(t *testing.T) {
	// Ordinary body text with no rulings must not trip the alignment path.
	doc := analyze(t, simpleDoc(), defaultOpts())
	if n := len(tableBlocks(doc)); n != 0 {
		t.Errorf("%d tables detected in prose", n)
	}
}

func TestTableOutputIsDeterministic(t *testing.T) {
	src := tableDoc(cells(
		[]string{"Region", "Revenue", "Growth"},
		[]string{"Northeast", "412000", "12 pct"},
		[]string{"Midwest", "298000", "4 pct"},
	))
	first, _ := buildDoc(t, src, defaultOpts())
	second, _ := buildDoc(t, src, defaultOpts())
	if !bytesEqual(first, second) {
		t.Error("two conversions of a table document differ byte for byte")
	}
}

func TestTwoColumnPageIsNotATable(t *testing.T) {
	// Spec 4.3 owns multi-column reading order. On a two-column page every
	// body line in the left column shares a baseline with one in the right,
	// so the alignment pass sees perfectly aligned rows. Calling that a table
	// flattens the page into rows read across instead of down.
	doc := analyze(t, twoColumnDoc(), defaultOpts())
	if n := len(tableBlocks(doc)); n != 0 {
		t.Errorf("%d tables detected on a two-column body page", n)
	}
}
