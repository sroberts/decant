package layout

import (
	"strings"
	"testing"

	"github.com/sroberts/decant/internal/pdf"
)

// glyphRun builds a horizontal run of glyphs starting at (x, y), advancing by
// each rune's width. A width of 0 uses half the size, which is close enough
// to a real font for clustering tests.
func glyphRun(text string, x, y, size float64) []pdf.Glyph {
	var out []pdf.Glyph
	for _, r := range text {
		adv := size * 0.5
		out = append(out, pdf.Glyph{
			X: x, Y: y, Advance: adv, Size: size, Rune: r, FontID: pdf.NoFont,
		})
		x += adv
	}
	return out
}

func TestAssembleLinesClustersByBaseline(t *testing.T) {
	var gs []pdf.Glyph
	gs = append(gs, glyphRun("first", 72, 100, 12)...)
	gs = append(gs, glyphRun("second", 72, 114, 12)...)
	gs = append(gs, glyphRun("third", 72, 128, 12)...)

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if len(pl.Lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(pl.Lines))
	}
	for i, want := range []string{"first", "second", "third"} {
		if pl.Lines[i].Text != want {
			t.Errorf("line %d = %q, want %q", i, pl.Lines[i].Text, want)
		}
	}
}

func TestAssembleLinesToleratesBaselineJitter(t *testing.T) {
	// Glyphs within 0.3 x median height of each other share a line. At size
	// 12 that is 3.6pt of tolerance.
	gs := glyphRun("abc", 72, 100, 12)
	gs = append(gs, pdf.Glyph{
		X: 100, Y: 101.5, Advance: 6, Size: 12, Rune: 'd', FontID: pdf.NoFont,
	})

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if len(pl.Lines) != 1 {
		t.Fatalf("got %d lines, want 1: %+v", len(pl.Lines), pl.Lines)
	}
	if !strings.Contains(pl.Lines[0].Text, "d") {
		t.Errorf("jittered glyph was lost: %q", pl.Lines[0].Text)
	}
}

func TestAssembleLinesSortsTopToBottom(t *testing.T) {
	// Feed the glyphs out of order; output must still read top to bottom.
	var gs []pdf.Glyph
	gs = append(gs, glyphRun("third", 72, 128, 12)...)
	gs = append(gs, glyphRun("first", 72, 100, 12)...)
	gs = append(gs, glyphRun("second", 72, 114, 12)...)

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	got := make([]string, len(pl.Lines))
	for i, l := range pl.Lines {
		got[i] = l.Text
	}
	want := []string{"first", "second", "third"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("lines = %v, want %v", got, want)
		}
	}
}

func TestSpaceInsertion(t *testing.T) {
	// Two words separated by a gap wider than the space threshold.
	gs := glyphRun("one", 72, 100, 12)
	gs = append(gs, glyphRun("two", 72+3*6+8, 100, 12)...)

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if len(pl.Lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(pl.Lines))
	}
	if pl.Lines[0].Text != "one two" {
		t.Errorf("text = %q, want %q", pl.Lines[0].Text, "one two")
	}
}

func TestNoSpuriousSpaceWithinAWord(t *testing.T) {
	// Glyphs laid out end to end must not gain interior spaces.
	gs := glyphRun("together", 72, 100, 12)
	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if len(pl.Lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(pl.Lines))
	}
	if pl.Lines[0].Text != "together" {
		t.Errorf("text = %q, want %q", pl.Lines[0].Text, "together")
	}
}

func TestInvisibleTextDroppedWhenVisibleExists(t *testing.T) {
	gs := glyphRun("visible", 72, 100, 12)
	hidden := glyphRun("hidden", 72, 120, 12)
	for i := range hidden {
		hidden[i].RenderMode = 3
	}
	gs = append(gs, hidden...)

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if pl.UsedInvisibleText {
		t.Error("UsedInvisibleText set on a page that has visible text")
	}
	for _, l := range pl.Lines {
		if strings.Contains(l.Text, "hidden") {
			t.Error("invisible text survived alongside visible text")
		}
	}
}

func TestInvisibleTextKeptWhenItIsAllThereIs(t *testing.T) {
	// The searchable-scan case from spec section 4.2.
	gs := glyphRun("ocr layer", 72, 100, 12)
	for i := range gs {
		gs[i].RenderMode = 3
	}

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if !pl.UsedInvisibleText {
		t.Error("UsedInvisibleText not set on a page whose only text is mode 3")
	}
	if len(pl.Lines) == 0 {
		t.Fatal("the invisible layer was dropped, leaving no text at all")
	}
	if !strings.Contains(pl.Lines[0].Text, "ocr") {
		t.Errorf("text = %q", pl.Lines[0].Text)
	}
}

func TestRotatedRunsSeparated(t *testing.T) {
	gs := glyphRun("upright", 72, 100, 12)
	rot := glyphRun("sideways", 30, 300, 10)
	for i := range rot {
		rot[i].Rotation = 90
	}
	gs = append(gs, rot...)

	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if len(pl.Rotated) == 0 {
		t.Fatal("rotated run was not separated")
	}
	for _, l := range pl.Lines {
		if strings.Contains(l.Text, "sideways") {
			t.Error("rotated text leaked into the upright lines")
		}
	}

	// With KeepRotated the run must appear in Lines instead.
	cfg := DefaultConfig()
	cfg.KeepRotated = true
	pl = AssembleLines(cfg, &pdf.PageContent{Glyphs: gs})
	found := false
	for _, l := range pl.Lines {
		if strings.Contains(l.Text, "sideways") {
			found = true
		}
	}
	if !found {
		t.Error("KeepRotated did not retain the rotated run")
	}
}

func TestDecodeFailuresCounted(t *testing.T) {
	gs := glyphRun("ab", 72, 100, 12)
	gs[1].Missing = true
	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if pl.DecodeFailures != 1 {
		t.Errorf("DecodeFailures = %d, want 1", pl.DecodeFailures)
	}
}

func TestNormalizeText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ﬁne", "fine"},
		{"ﬂow", "flow"},
		{"aﬀect", "affect"},
		{"eﬃcient", "efficient"},
		{"soft\u00adhyphen", "softhyphen"},
		{"zero\u200bwidth", "zerowidth"},
		{"\ufeffbom", "bom"},
		{"collapse   spaces", "collapse spaces"},
		{"trailing   ", "trailing"},
	}
	for _, c := range cases {
		if got := normalizeText(c.in); got != c.want {
			t.Errorf("normalizeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeTextAppliesNFC(t *testing.T) {
	// A decomposed e-acute must compose to the single code point.
	got := normalizeText("é")
	if got != "é" {
		t.Errorf("normalizeText of decomposed e-acute = %q (% x), want é", got, []rune(got))
	}
}

func TestEmptyInputIsSafe(t *testing.T) {
	if pl := AssembleLines(DefaultConfig(), nil); pl == nil || len(pl.Lines) != 0 {
		t.Error("nil page content did not yield an empty result")
	}
	if pl := AssembleLines(DefaultConfig(), &pdf.PageContent{}); len(pl.Lines) != 0 {
		t.Error("empty page content did not yield an empty result")
	}
}

func TestWhitespaceOnlyLinesDropped(t *testing.T) {
	gs := glyphRun("   ", 72, 100, 12)
	pl := AssembleLines(DefaultConfig(), &pdf.PageContent{Glyphs: gs})
	if len(pl.Lines) != 0 {
		t.Errorf("got %d lines from whitespace-only glyphs, want 0", len(pl.Lines))
	}
}

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{5}, 5},
		{[]float64{1, 2, 3}, 2},
		{[]float64{1, 2, 3, 4}, 2.5},
		{[]float64{3, 1, 2}, 2},
	}
	for _, c := range cases {
		if got := median(c.in); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMedianDoesNotMutateInput(t *testing.T) {
	in := []float64{3, 1, 2}
	median(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Errorf("median sorted the caller's slice: %v", in)
	}
}
