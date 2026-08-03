package layout

import (
	"unicode"

	"github.com/sroberts/decant/internal/pdf"
)

// StyleSpan is a run of a paragraph's text set in bold, italic, or both.
//
// Spec section 4.6 derives the two from the font's /FontDescriptor flags and
// its family-name suffix, which the font machinery already resolves; this
// turns that per-glyph fact into the character ranges the renderer needs.
type StyleSpan struct {
	// Start and End are byte offsets into Paragraph.Text, half-open.
	Start, End   int
	Bold, Italic bool
}

// MapStyles returns the bold and italic runs of a paragraph's text.
//
// Runs are built per line and merged across the line joins, because a phrase
// emphasised across a line break is one run in the joined text and emitting
// it as two would close and reopen the element mid-phrase.
//
// A run covering the whole paragraph is dropped. Emphasis is a contrast with
// the surrounding text, and a document set entirely in a font whose name ends
// in "Italic" is not emphasising anything; wrapping every paragraph would
// produce markup that says nothing and costs bytes against the chunk budget.
func MapStyles(cfg Config, p *Paragraph, fonts []*pdf.Font) []StyleSpan {
	if p == nil || len(p.Lines) != len(p.LineStarts) {
		return nil
	}

	var out []StyleSpan
	for i, l := range p.Lines {
		base := p.LineStarts[i]
		if base < 0 || len(l.Glyphs) == 0 {
			continue
		}

		_, offsets, _ := writeGlyphs(cfg, l.Glyphs, fonts,
			l.Baseline, l.Size, medianAdvance(l.Glyphs), true)
		if len(offsets) != len(l.Glyphs) {
			continue
		}
		lead := leadingSpace(l.Text)

		// Walk the line's glyphs, closing a run whenever the style changes.
		runStart, runBold, runItalic, open := 0, false, false, false
		flush := func(endGlyph int) {
			if !open || (!runBold && !runItalic) {
				open = false
				return
			}
			s := base + offsets[runStart] - lead
			g := l.Glyphs[endGlyph]
			e := base + offsets[endGlyph] + len(string(g.Rune)) - lead
			if s < 0 {
				s = 0
			}
			if e > len(p.Text) {
				e = len(p.Text)
			}
			if s < e {
				out = append(out, StyleSpan{Start: s, End: e, Bold: runBold, Italic: runItalic})
			}
			open = false
		}

		for gi, g := range l.Glyphs {
			bold, italic := glyphStyle(g, fonts)
			if !open {
				runStart, runBold, runItalic, open = gi, bold, italic, true
				continue
			}
			if bold != runBold || italic != runItalic {
				flush(gi - 1)
				runStart, runBold, runItalic, open = gi, bold, italic, true
			}
		}
		if open {
			flush(len(l.Glyphs) - 1)
		}
	}

	out = mergeAdjacentStyles(out)
	if len(out) == 1 && out[0].Start == 0 && out[0].End == len(p.Text) {
		return nil
	}

	// Runs shorter than a word are dropped. Emphasis applies to something a
	// reader can read; a single italic letter mid-sentence is a variable, a
	// symbol, or a font the producer switched to for one glyph, and marking
	// it up says nothing while costing an element.
	kept := out[:0]
	for _, sp := range out {
		if countLetters(p.Text[sp.Start:sp.End]) < cfg.StyleMinLetters {
			continue
		}
		kept = append(kept, sp)
	}
	return kept
}

// countLetters counts the letters in s, ignoring the superscript sentinels
// and any punctuation a run happens to span.
func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) && !(r >= 0xE000 && r <= 0xF8FF) {
			n++
		}
	}
	return n
}

// glyphStyle resolves a glyph's font to its bold and italic flags.
func glyphStyle(g pdf.Glyph, fonts []*pdf.Font) (bold, italic bool) {
	if int(g.FontID) >= len(fonts) {
		return false, false
	}
	f := fonts[g.FontID]
	if f == nil {
		return false, false
	}
	// A TeX math family sets italic on every variable it draws. That is a
	// typesetting convention for mathematics, not emphasis, and honouring it
	// wrapped 6,947 single letters on the corpus's textbook.
	if f.Math() {
		return f.Bold, false
	}
	return f.Bold, f.Italic
}

// mergeAdjacentStyles joins runs that meet, or that are separated only by the
// space a line join inserted, when they carry the same style.
func mergeAdjacentStyles(spans []StyleSpan) []StyleSpan {
	if len(spans) < 2 {
		return spans
	}
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if last.Bold == s.Bold && last.Italic == s.Italic && s.Start <= last.End+1 {
			if s.End > last.End {
				last.End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// leadingSpace returns the byte length of s's leading whitespace, which
// LineStarts has already trimmed off.
func leadingSpace(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}
