package layout

import (
	"strings"
	"testing"

	"github.com/sroberts/decant/internal/pdf"
)

// gappedRun lays out one glyph per rune at a fixed pitch, inserting a wide gap
// wherever the text has a space, which is what a real content stream does.
func gappedRun(text string, x, size, gap float64) []pdf.Glyph {
	var gs []pdf.Glyph
	for _, r := range text {
		if r == ' ' {
			x += gap
			continue
		}
		gs = append(gs, pdf.Glyph{
			Rune: r, X: x, Y: 100, Advance: size * 0.5, Size: size,
		})
		x += size * 0.5
	}
	return gs
}

func TestGlyphOffsetsLandOnTheirRunes(t *testing.T) {
	// Every offset must point at the glyph's own rune in the assembled text.
	// Spaces are the interesting case: normalizeText collapses runs of them
	// and trims the right edge, so a prefix normalized on its own loses a
	// space the whole string keeps.
	cfg := DefaultConfig()
	for _, text := range []string{
		"see section four",
		"a  wide   gap",
		"trailing space ",
		"single",
	} {
		gs := gappedRun(text, 0, 10, 40)
		raw, offsets, _ := writeGlyphs(cfg, gs, nil, 100, 10, 5, true)
		got := normalizeText(raw)

		if len(offsets) != len(gs) {
			t.Fatalf("%q: %d offsets for %d glyphs", text, len(offsets), len(gs))
		}
		for i, g := range gs {
			off := offsets[i]
			if off < 0 || off >= len(got) {
				t.Errorf("%q: glyph %d (%q) offset %d out of range for %q",
					text, i, g.Rune, off, got)
				continue
			}
			if r := []rune(got[off:])[0]; r != g.Rune {
				t.Errorf("%q: glyph %d offset %d points at %q, want %q (text %q)",
					text, i, off, r, g.Rune, got)
			}
		}
	}
}

func TestGlyphOffsetsSurviveLigatures(t *testing.T) {
	// A ligature decomposes into two runes, so every offset after it shifts.
	cfg := DefaultConfig()
	gs := gappedRun("aﬁn", 0, 10, 40) // a, ﬁ, n
	raw, offsets, _ := writeGlyphs(cfg, gs, nil, 100, 10, 5, true)
	got := normalizeText(raw)

	if !strings.Contains(got, "fi") {
		t.Fatalf("ligature did not decompose: %q", got)
	}
	// The final glyph must still point at 'n'.
	last := offsets[len(offsets)-1]
	if last >= len(got) || []rune(got[last:])[0] != 'n' {
		t.Errorf("offset %d in %q does not point at 'n'", last, got)
	}
}

func TestLineStartsPointAtEachLine(t *testing.T) {
	// The offsets a link maps through are only as good as the paragraph's
	// record of where each line landed, and dehyphenation moves that record
	// by editing text already emitted.
	cfg := DefaultConfig()

	lines := []Line{
		mkLine("the quick brown fox jumps over", 100),
		mkLine("the lazy dog and keeps going on", 114),
		mkLine("to a third line of prose here", 128),
	}
	p := buildParagraph(cfg, lines)

	if len(p.LineStarts) != len(lines) {
		t.Fatalf("%d starts for %d lines", len(p.LineStarts), len(lines))
	}
	for i, l := range lines {
		start := p.LineStarts[i]
		if start < 0 {
			t.Errorf("line %d has no start", i)
			continue
		}
		want := strings.TrimSpace(l.Text)
		if start+len(want) > len(p.Text) {
			t.Errorf("line %d start %d runs past the text", i, start)
			continue
		}
		if got := p.Text[start : start+len(want)]; got != want {
			t.Errorf("line %d start %d gives %q, want %q", i, start, got, want)
		}
	}
}

func TestLineStartsSurviveDehyphenation(t *testing.T) {
	// Dropping a line-break hyphen shortens the piece already emitted, so
	// every later offset shifts. If that correction is missed, links on the
	// lines after a dehyphenated break land one byte late.
	cfg := DefaultConfig()
	// Always drop, so the edit definitely happens.
	cfg.Dehyphenator = alwaysDrop{}

	lines := []Line{
		mkLine("a paragraph about hyphen-", 100),
		mkLine("ation and how it behaves", 114),
		mkLine("across a following line", 128),
	}
	p := buildParagraph(cfg, lines)

	last := p.LineStarts[len(lines)-1]
	want := strings.TrimSpace(lines[len(lines)-1].Text)
	if last < 0 || last+len(want) > len(p.Text) {
		t.Fatalf("last line start %d is out of range for %q", last, p.Text)
	}
	if got := p.Text[last : last+len(want)]; got != want {
		t.Errorf("after dehyphenation the last line start gives %q, want %q\nfull: %q",
			got, want, p.Text)
	}
}

func TestMapLinksCoversOnlyTheLinkedWords(t *testing.T) {
	// The point of mapping glyph by glyph: a cross-reference is a few words
	// inside a running line, and matching on the line would link all of it.
	cfg := DefaultConfig()
	line := mkLine("please see section four for more", 100)
	p := buildParagraph(cfg, []Line{line})

	// Cover exactly "section four".
	i := strings.Index(line.Text, "section")
	j := strings.Index(line.Text, " for")
	rect := rectOverGlyphs(line, i, j)

	spans := MapLinks(cfg, &p, nil, []pdf.Link{{Rect: rect, TargetPage: 3}})
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if got := p.Text[spans[0].Start:spans[0].End]; got != "section four" {
		t.Errorf("linked %q, want %q (text %q)", got, "section four", p.Text)
	}
	if spans[0].TargetPage != 3 {
		t.Errorf("target page %d, want 3", spans[0].TargetPage)
	}
}

func TestMapLinksIgnoresParagraphsItDoesNotTouch(t *testing.T) {
	cfg := DefaultConfig()
	p := buildParagraph(cfg, []Line{mkLine("a line of ordinary prose", 100)})
	far := pdf.Rect{MinX: 5000, MinY: 5000, MaxX: 5100, MaxY: 5100}

	if spans := MapLinks(cfg, &p, nil, []pdf.Link{{Rect: far, TargetPage: 1}}); len(spans) != 0 {
		t.Errorf("got %d spans for a link nowhere near the paragraph", len(spans))
	}
}

func TestMapLinksSpansALineBreak(t *testing.T) {
	// A single annotation rectangle covering two lines is a bounding box over
	// both, so the span runs from the first covered glyph to the last across
	// the join. One span, not one per line: the lines are contiguous in the
	// joined text and two anchors would break the phrase in half.
	//
	// Real files usually avoid this by emitting one annotation per line, or
	// by carrying /QuadPoints. decant reads /Rect only, so a genuine
	// multi-line annotation over-links to its bounding box.
	cfg := DefaultConfig()
	l1 := mkLine("text running to the very end", 100)
	l2 := mkLine("continuing onto the next line", 114)
	p := buildParagraph(cfg, []Line{l1, l2})

	rect := l1.Bounds.Union(l2.Bounds)
	spans := MapLinks(cfg, &p, nil, []pdf.Link{{Rect: rect, TargetPage: 2}})
	if len(spans) != 1 {
		t.Fatalf("got %d spans across a line break, want 1", len(spans))
	}

	got := p.Text[spans[0].Start:spans[0].End]
	if !strings.Contains(got, "very end") || !strings.Contains(got, "continuing") {
		t.Errorf("span %q does not reach across the break (text %q)", got, p.Text)
	}
}

func TestOverlappingLinksDoNotNest(t *testing.T) {
	// Nested anchors are invalid XHTML.
	cfg := DefaultConfig()
	line := mkLine("one two three four five six", 100)
	p := buildParagraph(cfg, []Line{line})

	wide := rectOverGlyphs(line, 0, len(line.Text))
	narrow := rectOverGlyphs(line, strings.Index(line.Text, "three"),
		strings.Index(line.Text, " four"))

	spans := MapLinks(cfg, &p, nil, []pdf.Link{
		{Rect: narrow, TargetPage: 1},
		{Rect: wide, TargetPage: 2},
	})
	for i := 1; i < len(spans); i++ {
		if spans[i].Start < spans[i-1].End {
			t.Errorf("spans %d and %d overlap: %v", i-1, i, spans)
		}
	}
}

// mkLine assembles a line at the given baseline from evenly pitched glyphs.
func mkLine(text string, y float64) Line {
	gs := gappedRun(text, 72, 10, 8)
	for i := range gs {
		gs[i].Y = y
	}
	l, ok := buildLine(DefaultConfig(), gs, nil)
	if !ok {
		panic("fixture line did not assemble: " + text)
	}
	return l
}

// rectOverGlyphs builds an annotation rectangle covering the glyphs whose
// runes lie in [from, to) of the line's text.
func rectOverGlyphs(l Line, from, to int) pdf.Rect {
	runeIdx := 0
	var r pdf.Rect
	first := true
	for _, g := range l.Glyphs {
		// Glyphs skip spaces, so walk the text alongside them.
		for runeIdx < len(l.Text) && l.Text[runeIdx] == ' ' {
			runeIdx++
		}
		if runeIdx >= from && runeIdx < to {
			box := pdf.Rect{
				MinX: g.X, MaxX: g.X + g.Advance,
				MinY: g.Y - g.Size, MaxY: g.Y + g.Size*0.5,
			}
			if first {
				r, first = box, false
			} else {
				r = r.Union(box)
			}
		}
		runeIdx += len(string(g.Rune))
	}
	return r
}

// alwaysDrop treats every line-break hyphen as a typesetting artifact.
type alwaysDrop struct{}

func (alwaysDrop) JoinFragments(left, right string) (bool, string) {
	return true, "test dehyphenator"
}
