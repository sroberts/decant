package decant

import (
	"github.com/sroberts/decant/internal/layout"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Marker classifications for list detection, per spec section 4.6: a block
// begins a list item when it opens with a bullet glyph, "N.", "N)", or a
// letter marker, and its continuation lines carry a hanging indent.

// bulletRunes are the glyphs typesetters use for unordered list markers.
const bulletRunes = "•◦‣⁃∙·–—-*‧●○■□▪▫➤➢»"

var (
	// orderedMarker matches "1.", "12)", "(3)", "iv.", "a)".
	orderedMarker = regexp.MustCompile(`^\(?([0-9]{1,3}|[ivxlcdm]{1,6}|[A-Za-z])[.)\]]\s+`)
	// romanOnly distinguishes a roman numeral from a single letter, which
	// changes the list type.
	romanOnly = regexp.MustCompile(`^[ivxlcdm]+$`)
	// footnoteMarker matches the openers spec 4.6 names for a footnote body.
	footnoteMarker = regexp.MustCompile(`^\s*[\[(]?([0-9]{1,3}|[*†‡§¶#]{1,3})[\])]?[.\s]`)
)

// listMarker describes the opener of a list item.
type listMarker struct {
	// ordered distinguishes <ol> from <ul>.
	ordered bool
	// start is the first item's number for an ordered list, 0 when unknown.
	start int
	// text is the item's content with the marker stripped.
	text string
	// width is the marker's length in runes, used to detect hanging indent.
	width int
}

// parseListMarker recognizes a list item opener.
func parseListMarker(s string) (listMarker, bool) {
	trimmed := strings.TrimLeft(s, " \t")
	if trimmed == "" {
		return listMarker{}, false
	}

	// Bullet: a single marker glyph followed by space.
	r := []rune(trimmed)
	if strings.ContainsRune(bulletRunes, r[0]) && len(r) > 1 && unicode.IsSpace(r[1]) {
		return listMarker{
			ordered: false,
			text:    strings.TrimSpace(string(r[1:])),
			width:   2,
		}, true
	}

	if m := orderedMarker.FindStringSubmatch(trimmed); m != nil {
		lm := listMarker{
			ordered: true,
			text:    strings.TrimSpace(trimmed[len(m[0]):]),
			width:   len([]rune(m[0])),
		}
		token := m[1]
		switch {
		case romanOnly.MatchString(strings.ToLower(token)) && len(token) > 1:
			// Roman numerals past "i" are unambiguous; a bare "i" is more
			// often a pronoun or a variable than a list marker.
			lm.start = romanValue(strings.ToLower(token))
		default:
			if n, err := strconv.Atoi(token); err == nil {
				lm.start = n
			}
		}
		if lm.text == "" {
			// "1." alone is a folio or a section number, not a list item.
			return listMarker{}, false
		}
		return lm, true
	}
	return listMarker{}, false
}

func romanValue(s string) int {
	vals := map[byte]int{'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000}
	total := 0
	for i := 0; i < len(s); i++ {
		v := vals[s[i]]
		if i+1 < len(s) && v < vals[s[i+1]] {
			total -= v
			continue
		}
		total += v
	}
	return total
}

// loneMarker matches a block consisting of nothing but a footnote marker.
var loneMarker = regexp.MustCompile(`^\s*[\[(]?([0-9]{1,3}|[*†‡§¶#]{1,3})[\])]?\.?\s*$`)

// mergeFootnoteMarkers joins a marker-only block to the note text beneath it.
//
// A footnote's marker is set raised and smaller than its body, which is enough
// of a size change for stage 4 to break the block between them. The result is
// one block holding "1" and another holding the note, and neither satisfies
// the footnote test in spec section 4.6, which expects the marker to open the
// body. Rejoining them is what makes the rule fire on real documents.
func (c *Converter) mergeFootnoteMarkers(
	blocks []Block,
	feats []blockFeatures,
	pageHeights map[int]float64,
) ([]Block, []blockFeatures) {
	h := c.opts.Heuristics
	outB := blocks[:0:0]
	outF := feats[:0:0]

	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		merged := false

		if !feats[i].isFigure && i+1 < len(blocks) && loneMarker.MatchString(b.Text) {
			next := blocks[i+1]
			ph := pageHeights[b.Page]
			sameBand := ph > 0 &&
				b.Bounds.MinY >= ph*(1-h.FootnoteBandRatio) &&
				next.Bounds.MinY >= ph*(1-h.FootnoteBandRatio)
			adjacent := next.Page == b.Page &&
				!feats[i+1].isFigure &&
				next.Bounds.MinX >= b.Bounds.MinX-1 &&
				absFloat(next.Bounds.MinY-b.Bounds.MinY) <
					2*maxFloat(feats[i].size, feats[i+1].size)

			if sameBand && adjacent && looksLikeProse(next.Text) {
				j := blocks[i+1]
				j.Text = strings.TrimSpace(b.Text) + " " + j.Text
				j.Bounds = unionRect(b.Bounds, j.Bounds)
				jf := feats[i+1]
				jf.words = countWords(j.Text)
				outB = append(outB, j)
				outF = append(outF, jf)
				i++
				merged = true
			}
		}

		if !merged {
			outB = append(outB, b)
			outF = append(outF, feats[i])
		}
	}
	return outB, outF
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// classifyStructure assigns the remaining block kinds from spec section 4.6:
// code, blockquote, list item, and footnote.
//
// It runs after headings so a heading is never reclassified, and before list
// grouping, which needs the per-item decisions in place.
func (c *Converter) classifyStructure(blocks []Block, feats []blockFeatures, body fontKey, rep *Report) {
	h := c.opts.Heuristics
	counts := map[BlockKind]int{}

	for i := range blocks {
		b := &blocks[i]
		f := &feats[i]
		if f.isFigure || b.Kind == KindHeading {
			continue
		}

		switch {
		case f.fixedPitch && !body.fixedPitch:
			// Spec 4.6: a fixed-pitch family is a code block. Comparing
			// against the body font matters, since a monospaced book would
			// otherwise be entirely code.
			b.Kind = KindCode

		case f.footnote:
			b.Kind = KindFootnote

		case f.listMarker:
			b.Kind = KindList

		case f.quoteIndent:
			b.Kind = KindQuote
		}
		if b.Kind != KindParagraph {
			counts[b.Kind]++
		}
	}

	for kind, n := range counts {
		rep.info("classify", -1, describeStructure(kind, n))
	}
	_ = h
}

func describeStructure(kind BlockKind, n int) string {
	switch kind {
	case KindCode:
		return plural(n, "code block")
	case KindFootnote:
		return plural(n, "footnote")
	case KindList:
		return plural(n, "list item")
	case KindQuote:
		return plural(n, "blockquote")
	}
	return plural(n, string(kind))
}

func plural(n int, noun string) string {
	s := "detected " + itoaInt(n) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}

func itoaInt(n int) string { return strconv.Itoa(n) }

// footnoteFeatures fills in the footnote test from spec section 4.6: the
// block sits in the bottom band, is set below the body size, and opens with a
// digit, dagger, or asterisk marker.
//
// All three conditions are required. Size alone would catch every caption,
// and a marker alone would catch numbered list items.
func (c *Converter) footnoteFeatures(b Block, f *blockFeatures, pageHeight float64, body fontKey) {
	h := c.opts.Heuristics
	if pageHeight <= 0 || body.size <= 0 {
		return
	}
	inBand := b.Bounds.MinY >= pageHeight*(1-h.FootnoteBandRatio)
	smaller := f.size < body.size*(1-h.FootnoteSizeRatio)
	marked := footnoteMarker.MatchString(b.Text)

	// A footnote body is prose. The content test is not in spec section 4.6,
	// but without it a low, small, mis-decoded run of digits such as
	// "2 2 2 2 2" satisfies all three stated conditions; on a mathematics
	// textbook that is a common shape.
	f.footnote = inBand && smaller && marked && looksLikeProse(b.Text)
}

// looksLikeProse reports whether text carries enough letters to be a sentence
// rather than stray symbols.
func looksLikeProse(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if letters >= minProseLetters {
				return true
			}
		}
	}
	return false
}

// minProseLetters is the letter count below which a block is treated as
// symbols rather than text.
const minProseLetters = 8

// quoteFeatures fills in the blockquote test: both margins inset beyond the
// body measure by more than QuoteIndentEm.
//
// bodyLeft and bodyRight are the document's modal text margins, which is what
// "beyond body" means; comparing against the page edges would call every
// indented paragraph a quote.
func (c *Converter) quoteFeatures(b Block, f *blockFeatures, bodyLeft, bodyRight float64) {
	if f.size <= 0 || bodyRight <= bodyLeft {
		return
	}
	// A blockquote is indented prose, so it wraps. The multi-line requirement
	// is not in spec section 4.6, and without it the rule fires on every
	// centered single line: a display equation, a centered caption, a byline.
	// On a mathematics textbook that turned 2,540 display equations into
	// blockquotes and left the body text in the minority.
	if f.lines < 2 {
		return
	}
	inset := c.opts.Heuristics.QuoteIndentEm * f.size
	f.quoteIndent = b.Bounds.MinX >= bodyLeft+inset && b.Bounds.MaxX <= bodyRight-inset
}

// groupLists merges consecutive list-item blocks into single list blocks.
//
// Spec section 4.6 groups consecutive items into <ul> or <ol> and infers the
// start attribute from the first marker. The items are joined into one block
// with newline separators, which the renderer turns back into <li> elements.
func groupLists(blocks []Block, feats []blockFeatures) ([]Block, []blockFeatures) {
	outB := blocks[:0:0]
	outF := feats[:0:0]

	for i := 0; i < len(blocks); {
		if blocks[i].Kind != KindList {
			outB = append(outB, blocks[i])
			outF = append(outF, feats[i])
			i++
			continue
		}

		// Collect the run of consecutive items sharing a list type and page.
		j := i
		ordered := feats[i].listOrdered
		var items []string
		bounds := blocks[i].Bounds
		for j < len(blocks) &&
			blocks[j].Kind == KindList &&
			feats[j].listOrdered == ordered &&
			blocks[j].Page == blocks[i].Page {
			items = append(items, feats[j].listText)
			bounds = unionRect(bounds, blocks[j].Bounds)
			j++
		}

		// A lone single-line item is not a list.
		//
		// Spec section 4.6 requires a hanging indent on the item's
		// continuation lines, which a one-line item cannot show. Rather than
		// waive the requirement outright, a single-liner qualifies only as
		// part of a run: "0. Auflage" on a title page is a byline, while
		// three consecutive numbered lines are a list.
		if j-i == 1 && feats[i].lines < 2 {
			blocks[i].Kind = KindParagraph
			outB = append(outB, blocks[i])
			outF = append(outF, feats[i])
			i = j
			continue
		}

		merged := blocks[i]
		merged.Text = strings.Join(items, "\n")
		merged.Bounds = bounds
		merged.ListOrdered = ordered
		merged.ListStart = feats[i].listStart
		merged.ListItems = items

		mf := feats[i]
		mf.words = countWords(merged.Text)
		outB = append(outB, merged)
		outF = append(outF, mf)
		i = j
	}
	return outB, outF
}

func unionRect(a, b Rect) Rect {
	if a == (Rect{}) {
		return b
	}
	if b == (Rect{}) {
		return a
	}
	return Rect{
		MinX: min64(a.MinX, b.MinX), MinY: min64(a.MinY, b.MinY),
		MaxX: max64(a.MaxX, b.MaxX), MaxY: max64(a.MaxY, b.MaxY),
	}
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// hasHangingIndent reports whether a block's continuation lines are indented
// past its first line, which is how a wrapped list item is set.
//
// Spec section 4.6 requires it alongside the marker: without it, a sentence
// opening "1. " in running prose would become a list.
func hasHangingIndent(lines []layout.Line) bool {
	if len(lines) < 2 {
		return false
	}
	first := lines[0].Indent()
	for _, l := range lines[1:] {
		// Half an em of tolerance, so a justified line that starts a hair
		// left of the others still counts.
		if l.Indent() <= first+lines[0].Size*0.25 {
			return false
		}
	}
	return true
}
