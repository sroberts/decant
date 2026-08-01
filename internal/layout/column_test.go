package layout

import (
	"strings"
	"testing"

	"github.com/sroberts/decant/internal/pdf"
)

// twoColumnGlyphs lays out two columns of body text with a gutter between
// them, optionally under a full-width heading.
func twoColumnGlyphs(headline string, leftLines, rightLines []string) []pdf.Glyph {
	const (
		size    = 10.0
		leading = 12.0
		leftX   = 72.0
		rightX  = 320.0
	)
	var gs []pdf.Glyph
	y := 100.0

	if headline != "" {
		// A continuous run across the whole measure, with no gap at the
		// gutter position.
		gs = append(gs, glyphRun(headline, leftX, y, 14)...)
		y += 24
	}
	top := y
	for _, l := range leftLines {
		gs = append(gs, glyphRun(l, leftX, y, size)...)
		y += leading
	}
	y = top
	for _, l := range rightLines {
		gs = append(gs, glyphRun(l, rightX, y, size)...)
		y += leading
	}
	return gs
}

// bodyLines produces column text short enough to leave a real gutter. At the
// 0.5-em advance glyphRun uses, 30 characters at size 10 is 150pt wide, so a
// column starting at 72 ends near 222 and the next starts at 320.
func bodyLines(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + " text fills the column"
	}
	return out
}

func TestDetectColumnsFindsTwo(t *testing.T) {
	gs := twoColumnGlyphs("", bodyLines(20, "left"), bodyLines(20, "right"))

	cols := DetectColumns(DefaultConfig(), gs, nil)
	if len(cols) != 2 {
		t.Fatalf("detected %d columns, want 2: %+v", len(cols), cols)
	}
	if cols[0].MaxX >= cols[1].MinX {
		t.Errorf("columns overlap: %+v", cols)
	}
}

func TestDetectColumnsSingleColumnStaysSingle(t *testing.T) {
	var gs []pdf.Glyph
	y := 100.0
	for i := 0; i < 30; i++ {
		gs = append(gs, glyphRun(
			"A single column of running body text across the full measure here", 72, y, 10)...)
		y += 12
	}

	cols := DetectColumns(DefaultConfig(), gs, nil)
	if len(cols) != 1 {
		t.Errorf("detected %d columns on single-column text, want 1: %+v", len(cols), cols)
	}
}

func TestDetectColumnsRaggedRightStaysSingle(t *testing.T) {
	// Ragged-right text leaves the right margin empty on many rows. That must
	// not read as a gutter, since bands touching the extent edge are margins.
	var gs []pdf.Glyph
	y := 100.0
	lengths := []int{60, 45, 58, 40, 62, 38, 55, 50, 61, 42}
	for i := 0; i < 30; i++ {
		n := lengths[i%len(lengths)]
		gs = append(gs, glyphRun(strings.Repeat("x", n), 72, y, 10)...)
		y += 12
	}

	cols := DetectColumns(DefaultConfig(), gs, nil)
	if len(cols) != 1 {
		t.Errorf("ragged-right text detected %d columns, want 1: %+v", len(cols), cols)
	}
}

func TestDetectColumnsRejectsLopsidedSplit(t *testing.T) {
	// One real column plus a sliver of marginal text must not split: the
	// sliver holds too few glyphs to be a column.
	var gs []pdf.Glyph
	y := 100.0
	for i := 0; i < 30; i++ {
		gs = append(gs, glyphRun("Main body text running along the measure", 72, y, 10)...)
		y += 12
	}
	gs = append(gs, glyphRun("note", 400, 140, 8)...)

	cols := DetectColumns(DefaultConfig(), gs, nil)
	if len(cols) != 1 {
		t.Errorf("detected %d columns despite a near-empty one, want 1: %+v", len(cols), cols)
	}
}

func TestForcedColumnCount(t *testing.T) {
	var gs []pdf.Glyph
	y := 100.0
	for i := 0; i < 30; i++ {
		gs = append(gs, glyphRun("A single column of running body text here", 72, y, 10)...)
		y += 12
	}

	cfg := DefaultConfig()
	cfg.Columns = 2
	cols := DetectColumns(cfg, gs, nil)
	if len(cols) != 2 {
		t.Errorf("--columns=2 gave %d columns: %+v", len(cols), cols)
	}

	// Forcing 1 must suppress detection on genuinely two-column input.
	cfg = DefaultConfig()
	cfg.Columns = 1
	two := twoColumnGlyphs("", bodyLines(20, "left"), bodyLines(20, "right"))
	cols = DetectColumns(cfg, two, nil)
	if len(cols) != 1 {
		t.Errorf("--columns=1 gave %d columns: %+v", len(cols), cols)
	}
}

func TestSplitLinesAtGutters(t *testing.T) {
	gs := twoColumnGlyphs("", bodyLines(6, "left"), bodyLines(6, "right"))
	pc := &pdf.PageContent{Glyphs: gs}

	pl := AssembleLines(DefaultConfig(), pc)
	cols := DetectColumns(DefaultConfig(), gs, nil)
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(cols))
	}

	// Before splitting, baseline clustering merged each pair of column lines.
	merged := 0
	for _, l := range pl.Lines {
		if strings.Contains(l.Text, "left") && strings.Contains(l.Text, "right") {
			merged++
		}
	}
	if merged == 0 {
		t.Skip("baseline clustering did not merge across columns in this fixture")
	}

	split := SplitLinesAtGutters(DefaultConfig(), pl.Lines, cols, nil)
	for _, l := range split {
		if strings.Contains(l.Text, "left") && strings.Contains(l.Text, "right") {
			t.Errorf("line still spans both columns after splitting: %q", l.Text)
		}
	}
}

func TestFullWidthHeadingSurvivesSplitting(t *testing.T) {
	// A continuous run across the gutter has no gap to split at, so it must
	// stay intact and be treated as full-width.
	gs := twoColumnGlyphs(
		"A Full Width Heading That Spans Both Of The Columns Below It Completely",
		bodyLines(10, "left"), bodyLines(10, "right"))

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	cols := DetectColumns(DefaultConfig(), gs, nil)
	split := SplitLinesAtGutters(DefaultConfig(), pl.Lines, cols, nil)

	found := false
	for _, l := range split {
		if strings.HasPrefix(l.Text, "A Full Width Heading") {
			found = true
			if !strings.HasSuffix(l.Text, "Completely") {
				t.Errorf("heading was truncated: %q", l.Text)
			}
			if len(cols) > 1 && columnOf(cols, l) != -1 {
				t.Errorf("heading was assigned to column %d, want full-width",
					columnOf(cols, l))
			}
		}
	}
	if !found {
		t.Error("the full-width heading did not survive splitting")
	}
}

func TestOrderLinesReadsColumnByColumn(t *testing.T) {
	gs := twoColumnGlyphs("", bodyLines(5, "left"), bodyLines(5, "right"))
	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	cols := DetectColumns(DefaultConfig(), gs, nil)
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(cols))
	}

	ordered := OrderLines(SplitLinesAtGutters(DefaultConfig(), pl.Lines, cols, nil), cols)

	// Every left-column line must precede every right-column line.
	lastLeft, firstRight := -1, len(ordered)
	for i, l := range ordered {
		switch {
		case strings.Contains(l.Text, "left"):
			lastLeft = i
		case strings.Contains(l.Text, "right"):
			if i < firstRight {
				firstRight = i
			}
		}
	}
	if lastLeft > firstRight {
		t.Errorf("columns interleaved: last left at %d, first right at %d\n%v",
			lastLeft, firstRight, texts(ordered))
	}
}

func TestOrderLinesTreatsFullWidthAsBarrier(t *testing.T) {
	// Heading, then two columns. The heading must come first, and the columns
	// must not be reordered around it.
	gs := twoColumnGlyphs(
		"Section Heading Spanning The Entire Measure Of This Page Right Here",
		bodyLines(6, "left"), bodyLines(6, "right"))

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	cols := DetectColumns(DefaultConfig(), gs, nil)
	ordered := OrderLines(SplitLinesAtGutters(DefaultConfig(), pl.Lines, cols, nil), cols)

	if len(ordered) == 0 {
		t.Fatal("no lines")
	}
	if !strings.HasPrefix(ordered[0].Text, "Section Heading") {
		t.Errorf("first line is %q, want the full-width heading", ordered[0].Text)
	}
}

func TestSegmentBlocksBreaksOnColumnChange(t *testing.T) {
	// Consecutive lines in reading order that sit in different columns have
	// no horizontal overlap and step backward vertically; both rules must
	// prevent a merge.
	lines := []Line{
		textLine("left column line one", 72, 100, 200, 10),
		textLine("left column line two", 72, 112, 200, 10),
		textLine("right column line one", 320, 100, 200, 10),
		textLine("right column line two", 320, 112, 200, 10),
	}
	bs := SegmentBlocks(DefaultConfig(), lines)
	if len(bs) != 2 {
		t.Fatalf("got %d blocks, want 2 (one per column)", len(bs))
	}
	for _, b := range bs {
		for _, l := range b.Lines {
			if strings.Contains(b.Lines[0].Text, "left") != strings.Contains(l.Text, "left") {
				t.Error("a block mixes lines from both columns")
			}
		}
	}
}

func TestSegmentBlocksBreaksOnHorizontalOffset(t *testing.T) {
	// A sidebar sitting beside body text at the same leading must not merge
	// into it, even though the vertical gap is small.
	lines := []Line{
		textLine("body text line one here", 72, 100, 200, 10),
		textLine("sidebar note off to the side", 400, 106, 150, 10),
	}
	bs := SegmentBlocks(DefaultConfig(), lines)
	if len(bs) != 2 {
		t.Errorf("got %d blocks, want 2; the overlap rule did not fire", len(bs))
	}
}

func TestAnalyzePageEndToEnd(t *testing.T) {
	gs := twoColumnGlyphs(
		"Paper Title Spanning The Whole Measure Across Both Of The Columns",
		bodyLines(8, "left"), bodyLines(8, "right"))

	pl := AnalyzePage(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if len(pl.Columns) != 2 {
		t.Fatalf("detected %d columns, want 2", len(pl.Columns))
	}
	if len(pl.Blocks) == 0 {
		t.Fatal("no blocks")
	}

	// The title is first and marked full-width.
	first := pl.Blocks[0]
	if !strings.HasPrefix(first.Lines[0].Text, "Paper Title") {
		t.Errorf("first block is %q, want the title", first.Lines[0].Text)
	}
	if first.Column != -1 {
		t.Errorf("title block column = %d, want -1 (full-width)", first.Column)
	}
}

func TestAnalyzePageEmptyInput(t *testing.T) {
	if pl := AnalyzePage(DefaultConfig(), nil); pl == nil || len(pl.Blocks) != 0 {
		t.Error("nil content did not yield an empty layout")
	}
	if pl := AnalyzePage(DefaultConfig(), &pdf.PageContent{}); len(pl.Blocks) != 0 {
		t.Error("empty content did not yield an empty layout")
	}
}

func texts(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}
