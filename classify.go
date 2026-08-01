package decant

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// blockFeatures carries the measurements structure classification needs,
// kept alongside Block rather than inside it so the public model stays clean.
type blockFeatures struct {
	size   float64
	family string
	bold   bool
	// words is the word count of the block's text.
	words int
	// terminal marks text ending in sentence-terminal punctuation.
	terminal bool
	// fullWidth marks a block spanning the page's gutters.
	fullWidth bool
	// lines is the number of assembled lines in the block.
	lines int
	// outlineForced marks a block whose level came from the PDF outline,
	// which is authoritative and must not be overwritten by inference.
	outlineForced bool
	// isFigure marks a block that carries an image rather than text, which
	// classification leaves alone.
	isFigure bool

	// fixedPitch marks a block set in a monospaced family, which spec 4.6
	// treats as code.
	fixedPitch bool
	// footnote, listMarker, and quoteIndent hold the remaining structure
	// tests from spec 4.6, filled in before classifyStructure runs.
	footnote    bool
	listMarker  bool
	quoteIndent bool
	// listOrdered, listStart, and listText carry the parsed marker.
	listOrdered bool
	listStart   int
	listText    string
}

// fontKey identifies a typographic style for the body font computation.
type fontKey struct {
	family string
	// size is quantized so that 9.96 and 10.02 land in the same bucket.
	size float64
	bold bool
	// fixedPitch marks a monospaced family. Code detection compares against
	// it, so a book set entirely in a monospaced face is not all code.
	fixedPitch bool
}

// sizeQuantum is the bucket width, in points, for grouping font sizes.
// Producers emit sizes that differ in the second decimal for what is
// visually one size; half a point groups those without merging real steps.
const sizeQuantum = 0.5

func quantizeSize(v float64) float64 {
	return math.Round(v/sizeQuantum) * sizeQuantum
}

// fontHistogram accumulates glyph counts per style across a document.
type fontHistogram map[fontKey]int

func (h fontHistogram) add(family string, size float64, bold, fixedPitch bool, glyphs int) {
	if glyphs <= 0 {
		return
	}
	h[fontKey{
		family: family, size: quantizeSize(size),
		bold: bold, fixedPitch: fixedPitch,
	}] += glyphs
}

// mode returns the glyph-count-weighted modal style, which spec section 4.6
// defines as the document's body font. Ties break deterministically by size,
// then family, then weight.
func (h fontHistogram) mode() (fontKey, bool) {
	if len(h) == 0 {
		return fontKey{}, false
	}
	keys := make([]fontKey, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if h[a] != h[b] {
			return h[a] > h[b]
		}
		if a.size != b.size {
			return a.size < b.size
		}
		if a.family != b.family {
			return a.family < b.family
		}
		return !a.bold && b.bold
	})
	return keys[0], true
}

// classify assigns a kind and heading level to every block.
//
// It runs after all pages are analyzed because the body font is a
// document-wide statistic, per spec section 4.6: computing it per page would
// make a chapter opening page, which is mostly display type, classify its own
// headings as body text.
func (c *Converter) classify(blocks []Block, feats []blockFeatures, hist fontHistogram, rep *Report) {
	body, ok := hist.mode()
	if !ok {
		return
	}
	rep.BodyFont = fmt.Sprintf("%s %.1fpt", body.family, body.size)
	if body.bold {
		rep.BodyFont += " bold"
	}

	h := c.opts.Heuristics
	threshold := body.size * (1 + h.HeadingSizeRatio)

	// First pass: decide which blocks are headings.
	isHeading := make([]bool, len(blocks))
	for i := range blocks {
		if feats[i].isFigure {
			continue
		}
		if feats[i].outlineForced {
			isHeading[i] = true
			continue
		}
		isHeading[i] = c.looksLikeHeading(feats[i], body, threshold)
	}

	// Second pass: rank the distinct heading styles by size, descending, and
	// map them onto h1 through h6.
	levels := rankHeadingStyles(blocks, feats, isHeading)

	headings := 0
	for i := range blocks {
		if feats[i].isFigure {
			continue
		}
		if !isHeading[i] {
			blocks[i].Kind = KindParagraph
			blocks[i].Level = 0
			continue
		}
		blocks[i].Kind = KindHeading
		headings++
		if blocks[i].Level > 0 {
			// The outline already fixed this level and outranks inference.
			continue
		}
		key := styleKey(feats[i])
		if lv, ok := levels[key]; ok {
			blocks[i].Level = lv
		} else {
			blocks[i].Level = 1
		}
	}

	if headings == 0 && len(blocks) > 4 {
		rep.warn("classify", -1,
			"no headings were detected; the document will convert as one chapter")
	}

	// The remaining kinds from spec 4.6 are decided against the same body
	// font, after headings so a heading is never reclassified.
	c.classifyStructure(blocks, feats, body, rep)
}

// looksLikeHeading applies the two tests from spec section 4.6.
func (c *Converter) looksLikeHeading(f blockFeatures, body fontKey, threshold float64) bool {
	h := c.opts.Heuristics

	// A block with no text cannot be a heading.
	if f.words == 0 {
		return false
	}

	// Test one: notably larger than the body font.
	//
	// The word cap is not in the spec. Section 4.6 makes size alone
	// sufficient, but a long passage set slightly large, an epigraph or a
	// pull quote, would then become a heading and split the book at it.
	// HeadingMaxWords is generous enough to only catch that case.
	if quantizeSize(f.size) >= quantizeSize(threshold) && f.words <= h.HeadingMaxWords {
		return true
	}

	// Test two: bold, short, and not a sentence.
	if f.bold && !body.bold && f.words < h.HeadingBoldMaxWords && !f.terminal {
		return true
	}
	return false
}

// styleKey is the grouping used to rank heading levels.
type headingStyle struct {
	size float64
	bold bool
}

func styleKey(f blockFeatures) headingStyle {
	return headingStyle{size: quantizeSize(f.size), bold: f.bold}
}

// rankHeadingStyles maps each distinct heading style onto a level, largest
// size first. Styles beyond the sixth collapse into h6, which is the deepest
// rank XHTML offers.
func rankHeadingStyles(blocks []Block, feats []blockFeatures, isHeading []bool) map[headingStyle]int {
	seen := map[headingStyle]bool{}
	for i := range blocks {
		if isHeading[i] {
			seen[styleKey(feats[i])] = true
		}
	}
	styles := make([]headingStyle, 0, len(seen))
	for s := range seen {
		styles = append(styles, s)
	}
	// Descending by size; at equal size, bold ranks above regular.
	sort.Slice(styles, func(i, j int) bool {
		if styles[i].size != styles[j].size {
			return styles[i].size > styles[j].size
		}
		return styles[i].bold && !styles[j].bold
	})

	levels := make(map[headingStyle]int, len(styles))
	for i, s := range styles {
		lv := i + 1
		if lv > 6 {
			lv = 6
		}
		levels[s] = lv
	}
	return levels
}

// outlineEntry is one flattened PDF bookmark.
type outlineEntry struct {
	title string
	page  int
	// userY is the destination's vertical position in PDF user space, NaN
	// when the destination gives none.
	userY float64
	// pageY is userY converted to page space, filled in while the target page
	// is loaded. NaN when unavailable.
	pageY float64
	depth int
}

// flattenOutline walks the bookmark tree depth first, recording nesting depth
// as the heading level the entry implies.
func flattenOutline(items []OutlineItem, depth int, out *[]outlineEntry) {
	for _, it := range items {
		if strings.TrimSpace(it.Title) != "" && it.Page >= 0 {
			*out = append(*out, outlineEntry{
				title: strings.TrimSpace(it.Title),
				page:  it.Page,
				userY: it.Y,
				pageY: math.NaN(),
				depth: depth,
			})
		}
		flattenOutline(it.Children, depth+1, out)
	}
}

// reconcileOutline forces block structure to match the PDF outline.
//
// Spec section 4.6 treats an existing outline as authoritative: each
// destination binds to the nearest block below it, that block's level becomes
// the outline depth, and the outline's title replaces the inferred text.
// Author-supplied structure beats anything inferred from type size.
func reconcileOutline(blocks []Block, feats []blockFeatures, entries []outlineEntry, rep *Report) {
	if len(entries) == 0 || len(blocks) == 0 {
		return
	}

	// Index blocks by page so each destination only scans its own page.
	byPage := map[int][]int{}
	for i := range blocks {
		byPage[blocks[i].Page] = append(byPage[blocks[i].Page], i)
	}

	used := map[int]bool{}
	matched := 0

	for _, e := range entries {
		candidates := byPage[e.page]
		if len(candidates) == 0 {
			continue
		}

		best := -1
		bestDist := math.Inf(1)
		for _, i := range candidates {
			if used[i] || feats[i].isFigure {
				continue
			}
			// Distance from the destination point to the block's vertical
			// span. A destination normally lands on the top edge of the
			// heading it names, and a block's span starts an ascent above its
			// baseline, so measuring to the span rather than to a single
			// coordinate is what makes the heading beat the paragraph under
			// it.
			var dist float64
			b := blocks[i].Bounds
			switch {
			case math.IsNaN(e.pageY):
				// No usable destination coordinate; take the first unused
				// block on the page.
				dist = float64(i)
			case e.pageY < b.MinY:
				dist = b.MinY - e.pageY
			case e.pageY > b.MaxY:
				// Entirely above the destination. Allow it as a last resort,
				// but penalize so anything at or below wins.
				dist = (e.pageY - b.MaxY) + 1e6
			default:
				// The destination falls inside this block.
				dist = 0
			}
			if dist < bestDist {
				best, bestDist = i, dist
			}
		}
		if best < 0 {
			continue
		}

		used[best] = true
		matched++

		lv := e.depth
		if lv < 1 {
			lv = 1
		}
		if lv > 6 {
			lv = 6
		}
		blocks[best].Kind = KindHeading
		blocks[best].Level = lv
		blocks[best].Text = e.title
		feats[best].outlineForced = true
	}

	rep.info("classify", -1, fmt.Sprintf(
		"reconciled %d of %d outline entries against the block tree",
		matched, len(entries)))
	if matched < len(entries)/2 {
		rep.warn("classify", -1, fmt.Sprintf(
			"only %d of %d outline entries matched a block; the table of contents may be incomplete",
			matched, len(entries)))
	}
}

// countWords returns the number of whitespace-separated words.
func countWords(s string) int { return len(strings.Fields(s)) }

// endsWithTerminal reports whether text ends in sentence-terminal
// punctuation, allowing a trailing closing quote or bracket.
func endsWithTerminal(s string) bool {
	s = strings.TrimRightFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\'' ||
			r == '”' || r == '’' || r == ')' || r == ']' || r == '»'
	})
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case '.', '!', '?':
		return true
	}
	for _, suffix := range []string{"…", "。", "！", "？"} {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}
