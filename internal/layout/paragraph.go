package layout

import (
	"math"
	"strings"
	"unicode"

	"github.com/sroberts/decant/internal/pdf"
)

// Paragraph is a run of lines forming one flowed unit of text.
type Paragraph struct {
	Text   string
	Lines  []Line
	Bounds pdf.Rect
	// Size is the median line size, which stage 6 compares against the
	// document body font.
	Size float64
	// Font is the dominant font across the paragraph.
	Font *pdf.Font
}

// Block is a group of lines that belong together: one visual unit within one
// column, before structure classification runs.
type Block struct {
	Lines  []Line
	Bounds pdf.Rect
	// Column is the index of the column the block sits in, or -1 for a
	// full-width block spanning the gutters.
	Column int
}

// SegmentBlocks groups lines into blocks with the bottom-up merge from spec
// section 4.4, which outperforms recursive XY-cut on documents with floats
// and sidebars.
//
// A line joins the running block when its horizontal overlap with the block
// exceeds BlockOverlapRatio of the narrower of the two and the vertical gap
// is within BlockGapRatio of the running median leading. A font family
// change, a size change beyond BlockSizeChangeRatio, or a backward step
// breaks the block.
//
// Lines must already be in reading order; OrderLines produces that. Sorting
// here would destroy the column ordering it establishes.
func SegmentBlocks(cfg Config, lines []Line) []Block {
	return segmentBlocks(cfg, lines, nil)
}

func segmentBlocks(cfg Config, lines []Line, cols []Column) []Block {
	if len(lines) == 0 {
		return nil
	}

	var blocks []Block
	var cur []Line
	// leadings holds the gaps seen so far in the running block, so its median
	// adapts to text set more loosely or tightly than the document norm.
	var leadings []float64
	// bounds tracks the running block's extent, which the overlap test needs:
	// comparing against the whole block rather than only the previous line
	// keeps a short line from detaching the rest of a paragraph.
	var bounds pdf.Rect

	flush := func() {
		if len(cur) == 0 {
			return
		}
		b := Block{Lines: cur, Bounds: bounds}
		if cols != nil {
			b.Column = columnOf(cols, cur[0])
		}
		blocks = append(blocks, b)
		cur = nil
		leadings = nil
		bounds = pdf.Rect{}
	}

	for _, l := range lines {
		if len(cur) == 0 {
			cur = append(cur, l)
			bounds = l.Bounds
			continue
		}
		prev := cur[len(cur)-1]
		gap := l.Baseline - prev.Baseline

		med := median(leadings)
		if med <= 0 {
			// No leading established yet; seed from the line size, which for
			// normally set text runs about 1.2 em.
			med = math.Max(prev.Size, l.Size) * 1.2
		}

		sizeChanged := relDiff(prev.Size, l.Size) > cfg.BlockSizeChangeRatio
		familyChanged := fontFamily(prev.Font) != fontFamily(l.Font)
		gapTooLarge := gap > cfg.BlockGapRatio*med
		// A non-positive gap means the reading order moved back up the page,
		// which happens at a column boundary.
		steppedBack := gap < 0
		overlapTooSmall := horizontalOverlap(bounds, l.Bounds) < cfg.BlockOverlapRatio

		if gapTooLarge || sizeChanged || familyChanged || steppedBack || overlapTooSmall {
			flush()
			cur = append(cur, l)
			bounds = l.Bounds
			continue
		}

		leadings = append(leadings, gap)
		cur = append(cur, l)
		bounds = bounds.Union(l.Bounds)
	}
	flush()
	return blocks
}

// horizontalOverlap returns the shared horizontal extent of two boxes as a
// fraction of the narrower one. Two boxes that do not overlap return 0; a box
// fully inside the other returns 1.
func horizontalOverlap(a, b pdf.Rect) float64 {
	lo := math.Max(a.MinX, b.MinX)
	hi := math.Min(a.MaxX, b.MaxX)
	if hi <= lo {
		return 0
	}
	narrower := math.Min(a.Width(), b.Width())
	if narrower <= 0 {
		return 1
	}
	return (hi - lo) / narrower
}

func relDiff(a, b float64) float64 {
	m := math.Max(math.Abs(a), math.Abs(b))
	if m == 0 {
		return 0
	}
	return math.Abs(a-b) / m
}

func fontFamily(f *pdf.Font) string {
	if f == nil {
		return ""
	}
	return f.Family
}

// Reconstruct turns a block's lines into paragraphs, following the rules in
// spec section 4.6.
//
// Dehyphenation is spec section 4.6 and lands in M4; line-break hyphens
// survive here verbatim.
func Reconstruct(cfg Config, b Block) []Paragraph {
	if len(b.Lines) == 0 {
		return nil
	}

	medIndent := medianIndent(b.Lines)
	blockWidth := b.Bounds.Width()

	var paras []Paragraph
	var cur []Line
	var leadings []float64

	flush := func() {
		if len(cur) == 0 {
			return
		}
		paras = append(paras, buildParagraph(cur))
		cur = nil
	}

	for i, l := range b.Lines {
		if i == 0 {
			cur = append(cur, l)
			continue
		}
		prev := b.Lines[i-1]
		gap := l.Baseline - prev.Baseline

		med := median(leadings)
		if med <= 0 {
			med = math.Max(prev.Size, l.Size) * 1.2
		}

		em := l.Size
		if em <= 0 {
			em = medianLineSize(b.Lines)
		}

		indented := l.Indent()-medIndent > cfg.ParagraphIndentEm*em
		gapped := gap > med*(1+cfg.ParagraphGapRatio)
		ended := endsSentence(prev.Text) &&
			blockWidth > 0 &&
			prev.Width() < cfg.ShortLineRatio*blockWidth

		if indented || gapped || ended {
			flush()
			cur = append(cur, l)
			leadings = nil
			continue
		}

		leadings = append(leadings, gap)
		cur = append(cur, l)
	}
	flush()
	return paras
}

func buildParagraph(lines []Line) Paragraph {
	p := Paragraph{Lines: lines}

	var sb strings.Builder
	for i, l := range lines {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(strings.TrimSpace(l.Text))
		p.Bounds = p.Bounds.Union(l.Bounds)
	}
	p.Text = strings.TrimSpace(sb.String())

	sizes := make([]float64, 0, len(lines))
	for _, l := range lines {
		sizes = append(sizes, l.Size)
	}
	p.Size = median(sizes)

	// Dominant font by line count, ties broken toward the first line so the
	// result does not depend on map iteration order.
	counts := map[*pdf.Font]int{}
	for _, l := range lines {
		counts[l.Font]++
	}
	best, bestN := lines[0].Font, 0
	for _, l := range lines {
		if n := counts[l.Font]; n > bestN {
			best, bestN = l.Font, n
		}
	}
	p.Font = best
	return p
}

// endsSentence reports whether the text ends with terminal punctuation,
// allowing for a trailing closing quote or bracket.
func endsSentence(s string) bool {
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
	// Multi-byte terminals.
	for _, suffix := range []string{"…", "。", "！", "？"} {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func medianIndent(lines []Line) float64 {
	v := make([]float64, 0, len(lines))
	for _, l := range lines {
		v = append(v, l.Indent())
	}
	return median(v)
}

func medianLineSize(lines []Line) float64 {
	v := make([]float64, 0, len(lines))
	for _, l := range lines {
		if l.Size > 0 {
			v = append(v, l.Size)
		}
	}
	return median(v)
}
