package layout

import (
	"sort"
	"strings"

	"github.com/sroberts/decant/internal/pdf"
)

// LinkSpan is a resolved link annotation's character range within one
// paragraph's text, per spec section 4.9.
type LinkSpan struct {
	// Start and End are byte offsets into Paragraph.Text, half-open.
	Start, End int
	// TargetPage is the zero-based destination page.
	TargetPage int
	// TargetY is the destination's vertical position in PDF user space, or
	// NaN. The consumer converts it through the target page's base CTM.
	TargetY float64
}

// MapLinks returns the character ranges of p's text covered by the given link
// rectangles.
//
// A link is matched glyph by glyph rather than by comparing rectangles to line
// bounds, because the common case is a few words inside a running paragraph:
// "see section 4.2" is one annotation over three words of a line that carries
// forty. Matching on the line would link the whole line.
//
// Links that cover no glyph of this paragraph return nothing, which is the
// normal case, since every link on the page is offered to every paragraph.
func MapLinks(cfg Config, p *Paragraph, fonts []*pdf.Font, links []pdf.Link) []LinkSpan {
	if p == nil || len(links) == 0 || len(p.Lines) != len(p.LineStarts) {
		return nil
	}

	var out []LinkSpan
	for _, link := range links {
		if !link.Rect.Intersects(p.Bounds) {
			continue
		}
		start, end, ok := spanOf(cfg, p, fonts, link)
		if !ok {
			continue
		}
		out = append(out, LinkSpan{
			Start: start, End: end,
			TargetPage: link.TargetPage, TargetY: link.TargetY,
		})
	}
	return resolveOverlaps(out)
}

// spanOf returns the paragraph-text range one link covers, as the union of its
// per-line ranges. A link broken across a line break yields one span rather
// than two, since the lines are contiguous in the joined text.
func spanOf(cfg Config, p *Paragraph, fonts []*pdf.Font, link pdf.Link) (int, int, bool) {
	lo, hi := -1, -1

	for i, l := range p.Lines {
		base := p.LineStarts[i]
		if base < 0 {
			continue
		}
		if !link.Rect.Intersects(l.Bounds) {
			continue
		}

		first, last := coveredGlyphs(l.Glyphs, link.Rect)
		if first < 0 {
			continue
		}

		_, offsets, _ := writeGlyphs(cfg, l.Glyphs, fonts,
			l.Baseline, l.Size, medianAdvance(l.Glyphs), true)
		if len(offsets) != len(l.Glyphs) {
			continue
		}

		// Offsets are into the line's own text; LineStarts refers to that
		// text after TrimSpace, so the leading trim has to come off.
		lead := len(l.Text) - len(strings.TrimLeft(l.Text, " \t"))

		s := base + offsets[first] - lead
		e := base + offsets[last] + len(string(l.Glyphs[last].Rune)) - lead
		if s < 0 {
			s = 0
		}
		if e > len(p.Text) {
			e = len(p.Text)
		}
		if s >= e {
			continue
		}
		if lo < 0 || s < lo {
			lo = s
		}
		if e > hi {
			hi = e
		}
	}

	if lo < 0 || hi <= lo {
		return 0, 0, false
	}
	return lo, hi, true
}

// coveredGlyphs returns the first and last index of the glyphs whose centers
// fall inside the rectangle, or -1 when none do.
//
// The center is the test point rather than the whole advance box: annotation
// rectangles are drawn generously and routinely clip a neighbouring glyph's
// edge, which an overlap test would then pull into the link.
func coveredGlyphs(gs []pdf.Glyph, r pdf.Rect) (int, int) {
	first, last := -1, -1
	for i, g := range gs {
		cx := g.X + g.Advance/2
		cy := g.Y - g.Size*0.25
		if cx < r.MinX || cx > r.MaxX || cy < r.MinY || cy > r.MaxY {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	return first, last
}

// resolveOverlaps sorts spans and drops any that overlap one already kept.
//
// Nested anchors are invalid XHTML, and two annotations over the same words
// happen in real files: a heading that is both an outline target and a
// cross-reference source, or a stale annotation left behind by an editor. The
// earlier span wins, and on a tie the longer one does, so the choice does not
// depend on the order the annotations happened to be stored in.
func resolveOverlaps(spans []LinkSpan) []LinkSpan {
	if len(spans) < 2 {
		return spans
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End-spans[i].Start > spans[j].End-spans[j].Start
	})

	out := spans[:0]
	prevEnd := -1
	for _, s := range spans {
		if s.Start < prevEnd {
			continue
		}
		out = append(out, s)
		prevEnd = s.End
	}
	return out
}
