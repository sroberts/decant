package layout

import (
	"strings"
	"testing"

	"github.com/sroberts/decant/internal/pdf"
)

// textLine builds a Line without going through glyph assembly, so paragraph
// rules can be tested in isolation.
func textLine(text string, x, baseline, width, size float64) Line {
	return Line{
		Text:     text,
		Baseline: baseline,
		Size:     size,
		Bounds: pdf.Rect{
			MinX: x, MaxX: x + width,
			MinY: baseline - size, MaxY: baseline,
		},
	}
}

func block(lines ...Line) Block {
	b := Block{Lines: lines}
	for _, l := range lines {
		b.Bounds = b.Bounds.Union(l.Bounds)
	}
	return b
}

func paraTexts(ps []Paragraph) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Text
	}
	return out
}

func TestReconstructJoinsWrappedLines(t *testing.T) {
	b := block(
		textLine("The quick brown fox jumps over", 72, 100, 400, 12),
		textLine("the lazy dog and keeps running", 72, 114, 400, 12),
		textLine("until it reaches the far fence.", 72, 128, 400, 12),
	)
	ps := Reconstruct(DefaultConfig(), b)
	if len(ps) != 1 {
		t.Fatalf("got %d paragraphs, want 1: %q", len(ps), paraTexts(ps))
	}
	want := "The quick brown fox jumps over the lazy dog and keeps running until it reaches the far fence."
	if ps[0].Text != want {
		t.Errorf("text = %q, want %q", ps[0].Text, want)
	}
}

func TestReconstructBreaksOnVerticalGap(t *testing.T) {
	b := block(
		textLine("First paragraph line one", 72, 100, 400, 12),
		textLine("first paragraph line two", 72, 114, 400, 12),
		// A gap well beyond the running leading starts a new paragraph.
		textLine("Second paragraph begins", 72, 150, 400, 12),
		textLine("second paragraph line two", 72, 164, 400, 12),
	)
	ps := Reconstruct(DefaultConfig(), b)
	if len(ps) != 2 {
		t.Fatalf("got %d paragraphs, want 2: %q", len(ps), paraTexts(ps))
	}
	if !strings.HasPrefix(ps[1].Text, "Second paragraph") {
		t.Errorf("second paragraph = %q", ps[1].Text)
	}
}

func TestReconstructBreaksOnIndent(t *testing.T) {
	// A first-line indent beyond half an em starts a new paragraph even at
	// normal leading.
	b := block(
		textLine("Body text running along here", 72, 100, 400, 12),
		textLine("and continuing on this line.", 72, 114, 400, 12),
		textLine("Indented start of the next one", 90, 128, 382, 12),
		textLine("continuing back at the margin.", 72, 142, 400, 12),
	)
	ps := Reconstruct(DefaultConfig(), b)
	if len(ps) != 2 {
		t.Fatalf("got %d paragraphs, want 2: %q", len(ps), paraTexts(ps))
	}
	if !strings.HasPrefix(ps[1].Text, "Indented start") {
		t.Errorf("second paragraph = %q", ps[1].Text)
	}
}

func TestReconstructBreaksOnShortLineEndingASentence(t *testing.T) {
	// A line ending in a period and filling well under the block width ends
	// the paragraph.
	b := block(
		textLine("A sentence that runs the full width of", 72, 100, 400, 12),
		textLine("the column and then stops here.", 72, 114, 200, 12),
		textLine("A new sentence starts on this line and", 72, 128, 400, 12),
		textLine("carries on to fill the measure fully.", 72, 142, 400, 12),
	)
	ps := Reconstruct(DefaultConfig(), b)
	if len(ps) != 2 {
		t.Fatalf("got %d paragraphs, want 2: %q", len(ps), paraTexts(ps))
	}
}

func TestReconstructKeepsShortLineWithoutTerminalPunctuation(t *testing.T) {
	// A short line that does not end a sentence is a wrap, not a break.
	b := block(
		textLine("A clause that runs the full width and", 72, 100, 400, 12),
		textLine("then breaks short but continues", 72, 114, 200, 12),
		textLine("onto the following line of text.", 72, 128, 400, 12),
	)
	ps := Reconstruct(DefaultConfig(), b)
	if len(ps) != 1 {
		t.Errorf("got %d paragraphs, want 1: %q", len(ps), paraTexts(ps))
	}
}

func TestEndsSentence(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"A sentence.", true},
		{"A question?", true},
		{"An exclamation!", true},
		{`He said "so."`, true},
		{"A parenthetical.)", true},
		{"Trailing space. ", true},
		{"An ellipsis…", true},
		{"No terminal punctuation", false},
		{"A clause,", false},
		{"A colon:", false},
		{"", false},
	}
	for _, c := range cases {
		if got := endsSentence(c.in); got != c.want {
			t.Errorf("endsSentence(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSegmentBlocksSplitsOnGap(t *testing.T) {
	lines := []Line{
		textLine("Block one line one", 72, 100, 400, 12),
		textLine("block one line two", 72, 114, 400, 12),
		// A large gap starts a new block.
		textLine("Block two line one", 72, 300, 400, 12),
		textLine("block two line two", 72, 314, 400, 12),
	}
	bs := SegmentBlocks(DefaultConfig(), lines)
	if len(bs) != 2 {
		t.Fatalf("got %d blocks, want 2", len(bs))
	}
	if len(bs[0].Lines) != 2 || len(bs[1].Lines) != 2 {
		t.Errorf("block sizes = %d and %d, want 2 and 2", len(bs[0].Lines), len(bs[1].Lines))
	}
}

func TestSegmentBlocksSplitsOnSizeChange(t *testing.T) {
	lines := []Line{
		textLine("A heading at a larger size", 72, 100, 400, 20),
		textLine("Body text at the usual size", 72, 124, 400, 12),
		textLine("more body text on this line", 72, 138, 400, 12),
	}
	bs := SegmentBlocks(DefaultConfig(), lines)
	if len(bs) != 2 {
		t.Fatalf("got %d blocks, want 2", len(bs))
	}
	if len(bs[0].Lines) != 1 {
		t.Errorf("the larger line did not stand alone: %d lines", len(bs[0].Lines))
	}
}

func TestSegmentBlocksSplitsOnFamilyChange(t *testing.T) {
	serif := &pdf.Font{Family: "Times"}
	sans := &pdf.Font{Family: "Helvetica"}

	l1 := textLine("Serif line here", 72, 100, 400, 12)
	l1.Font = serif
	l2 := textLine("Still serif here", 72, 114, 400, 12)
	l2.Font = serif
	l3 := textLine("Now sans serif", 72, 128, 400, 12)
	l3.Font = sans

	bs := SegmentBlocks(DefaultConfig(), []Line{l1, l2, l3})
	if len(bs) != 2 {
		t.Fatalf("got %d blocks, want 2", len(bs))
	}
}

func TestSegmentBlocksEmptyInput(t *testing.T) {
	if bs := SegmentBlocks(DefaultConfig(), nil); bs != nil {
		t.Errorf("got %v, want nil for empty input", bs)
	}
}

func TestReconstructEmptyBlock(t *testing.T) {
	if ps := Reconstruct(DefaultConfig(), Block{}); ps != nil {
		t.Errorf("got %v, want nil for an empty block", ps)
	}
}
