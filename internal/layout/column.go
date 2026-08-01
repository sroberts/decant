package layout

import (
	"math"
	"sort"

	"github.com/sroberts/decant/internal/pdf"
)

// Column is a vertical text region bounded horizontally by gutters.
type Column struct {
	MinX, MaxX float64
}

// Contains reports whether x falls inside the column.
func (c Column) Contains(x float64) bool { return x >= c.MinX && x <= c.MaxX }

// Width returns the column's horizontal extent.
func (c Column) Width() float64 { return c.MaxX - c.MinX }

// projectionBins is the horizontal resolution of the density profile. At 512
// bins a US Letter page resolves to roughly 1.2pt, finer than any gutter
// worth detecting.
const projectionBins = 512

// DetectColumns finds the page's text columns from a vertical projection
// profile of glyph density, per spec section 4.4.
//
// The profile is computed per row rather than over the whole page height:
// a full-width title or figure crosses the gutter and would otherwise fill
// it in a flat projection. A bin counts as gutter when it is empty across at
// least GutterMinHeightRatio of the rows that carry any text at all.
//
// cfg.Columns overrides detection: 1 forces a single column, 2 or 3 keep only
// the strongest gutters, and 0 detects.
func DetectColumns(cfg Config, glyphs []pdf.Glyph, fonts []*pdf.Font) []Column {
	if len(glyphs) == 0 {
		return nil
	}

	minX, maxX, minY, maxY := textExtent(glyphs)
	full := []Column{{MinX: minX, MaxX: maxX}}
	if cfg.Columns == 1 || maxX-minX <= 0 || maxY-minY <= 0 {
		return full
	}

	gutters := findGutters(cfg, glyphs, fonts, minX, maxX, minY, maxY)
	if len(gutters) == 0 {
		if cfg.Columns > 1 {
			// The caller asserted a column count the profile does not show.
			// An even split is a better guess than ignoring the flag.
			return evenColumns(minX, maxX, cfg.Columns)
		}
		return full
	}

	// Keep the strongest gutters when a count is forced or the profile found
	// more than the maximum.
	limit := cfg.MaxColumns - 1
	if cfg.Columns > 1 {
		limit = cfg.Columns - 1
	}
	if limit < 1 {
		return full
	}
	if len(gutters) > limit {
		sort.SliceStable(gutters, func(i, j int) bool {
			return gutters[i].strength > gutters[j].strength
		})
		gutters = gutters[:limit]
	}
	sort.SliceStable(gutters, func(i, j int) bool { return gutters[i].minX < gutters[j].minX })

	cols := columnsBetween(gutters, minX, maxX)
	if cfg.Columns > 1 && len(cols) != cfg.Columns {
		return evenColumns(minX, maxX, cfg.Columns)
	}

	// A split that leaves a column nearly empty is a false positive, most
	// often a hanging indent or a centered heading read as a gutter.
	if !columnsCarryText(cfg, cols, glyphs) {
		return full
	}
	return cols
}

// gutter is a candidate vertical whitespace band.
type gutter struct {
	minX, maxX float64
	// strength ranks candidates when more are found than the column count
	// allows: wider and emptier bands win.
	strength float64
}

// findGutters computes the per-row occupancy profile and extracts the bands
// that qualify as gutters.
func findGutters(cfg Config, glyphs []pdf.Glyph, fonts []*pdf.Font, minX, maxX, minY, maxY float64) []gutter {
	binW := (maxX - minX) / projectionBins
	if binW <= 0 {
		return nil
	}

	rowH := medianGlyphSize(glyphs) * 1.2
	if rowH <= 0 {
		rowH = 12
	}
	nRows := int((maxY-minY)/rowH) + 1
	if nRows < 1 {
		nRows = 1
	}

	// occupied[row] is the set of bins carrying ink on that row.
	occupied := make([][]bool, nRows)
	for i := range occupied {
		occupied[i] = make([]bool, projectionBins)
	}
	rowHasText := make([]bool, nRows)

	for _, g := range glyphs {
		row := int((g.Y - minY) / rowH)
		if row < 0 {
			row = 0
		}
		if row >= nRows {
			row = nRows - 1
		}
		rowHasText[row] = true

		x0, x1 := g.X, g.X+g.Advance
		if x1 < x0 {
			x0, x1 = x1, x0
		}
		b0 := int((x0 - minX) / binW)
		b1 := int((x1 - minX) / binW)
		if b0 < 0 {
			b0 = 0
		}
		if b1 >= projectionBins {
			b1 = projectionBins - 1
		}
		for b := b0; b <= b1; b++ {
			occupied[row][b] = true
		}
	}

	textRows := 0
	for _, has := range rowHasText {
		if has {
			textRows++
		}
	}
	if textRows == 0 {
		return nil
	}

	// emptyFrac[bin] is the fraction of text-carrying rows where the bin is
	// clear. A gutter is clear nearly everywhere; body text is not.
	emptyFrac := make([]float64, projectionBins)
	for b := 0; b < projectionBins; b++ {
		clear := 0
		for r := 0; r < nRows; r++ {
			if !rowHasText[r] {
				continue
			}
			if !occupied[r][b] {
				clear++
			}
		}
		emptyFrac[b] = float64(clear) / float64(textRows)
	}

	minWidth := cfg.GutterMinWidthSpaces * medianSpaceWidth(glyphs, fonts)
	if minWidth <= 0 {
		minWidth = cfg.GutterMinWidthSpaces * medianGlyphSize(glyphs) * 0.25
	}

	var out []gutter
	b := 0
	for b < projectionBins {
		if emptyFrac[b] < cfg.GutterMinHeightRatio {
			b++
			continue
		}
		start := b
		sum := 0.0
		for b < projectionBins && emptyFrac[b] >= cfg.GutterMinHeightRatio {
			sum += emptyFrac[b]
			b++
		}
		end := b // exclusive

		// Bands touching either edge of the text extent are margins, not
		// gutters, and splitting on them would produce an empty column.
		if start == 0 || end == projectionBins {
			continue
		}

		gx0 := minX + float64(start)*binW
		gx1 := minX + float64(end)*binW
		if gx1-gx0 < minWidth {
			continue
		}
		out = append(out, gutter{
			minX:     gx0,
			maxX:     gx1,
			strength: (gx1 - gx0) * (sum / float64(end-start)),
		})
	}
	return out
}

// columnsBetween turns gutter bands into the columns they separate.
func columnsBetween(gutters []gutter, minX, maxX float64) []Column {
	cols := make([]Column, 0, len(gutters)+1)
	left := minX
	for _, g := range gutters {
		if g.minX > left {
			cols = append(cols, Column{MinX: left, MaxX: g.minX})
		}
		left = g.maxX
	}
	if maxX > left {
		cols = append(cols, Column{MinX: left, MaxX: maxX})
	}
	if len(cols) == 0 {
		return []Column{{MinX: minX, MaxX: maxX}}
	}
	return cols
}

func evenColumns(minX, maxX float64, n int) []Column {
	if n < 1 {
		n = 1
	}
	w := (maxX - minX) / float64(n)
	cols := make([]Column, 0, n)
	for i := 0; i < n; i++ {
		cols = append(cols, Column{
			MinX: minX + float64(i)*w,
			MaxX: minX + float64(i+1)*w,
		})
	}
	return cols
}

// columnsCarryText rejects a split where any column holds too little text to
// be real. Spec section 4.4 warns that the heuristic misfires on tables and
// figures; this is the guard against that.
func columnsCarryText(cfg Config, cols []Column, glyphs []pdf.Glyph) bool {
	if len(cols) < 2 {
		return true
	}
	counts := make([]int, len(cols))
	total := 0
	for _, g := range glyphs {
		c := columnIndexOf(cols, g.X+g.Advance/2)
		if c < 0 {
			continue
		}
		counts[c]++
		total++
	}
	if total == 0 {
		return false
	}
	for _, n := range counts {
		if float64(n)/float64(total) < cfg.ColumnMinGlyphRatio {
			return false
		}
	}
	return true
}

// columnIndexOf returns the column containing x, or the nearest one when x
// falls inside a gutter. It returns -1 only when there are no columns.
func columnIndexOf(cols []Column, x float64) int {
	if len(cols) == 0 {
		return -1
	}
	for i, c := range cols {
		if c.Contains(x) {
			return i
		}
	}
	best, bestDist := 0, math.Inf(1)
	for i, c := range cols {
		d := math.Min(math.Abs(x-c.MinX), math.Abs(x-c.MaxX))
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// textExtent returns the bounding box of the glyph advances.
func textExtent(glyphs []pdf.Glyph) (minX, maxX, minY, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, g := range glyphs {
		x0, x1 := g.X, g.X+g.Advance
		if x1 < x0 {
			x0, x1 = x1, x0
		}
		minX = math.Min(minX, x0)
		maxX = math.Max(maxX, x1)
		minY = math.Min(minY, g.Y)
		maxY = math.Max(maxY, g.Y)
	}
	if math.IsInf(minX, 1) {
		return 0, 0, 0, 0
	}
	return minX, maxX, minY, maxY
}

// medianSpaceWidth returns the median width of a space in page units across
// the glyphs' fonts.
func medianSpaceWidth(glyphs []pdf.Glyph, fonts []*pdf.Font) float64 {
	var widths []float64
	for _, g := range glyphs {
		f := fontOf(g, fonts)
		if f == nil {
			continue
		}
		if sw, ok := f.SpaceWidth(); ok && sw > 0 {
			widths = append(widths, sw/1000*g.Size)
		}
	}
	return median(widths)
}

// SplitLinesAtGutters breaks lines that straddle a gutter into per-column
// fragments.
//
// A two-column body line is two text runs sharing a baseline with a wide gap
// at the gutter, so it splits. A full-width title is continuous text across
// the gutter with no gap there, so it survives intact and is later ordered as
// a band barrier. That distinction is what keeps headings from being sliced
// in half.
func SplitLinesAtGutters(cfg Config, lines []Line, cols []Column, fonts []*pdf.Font) []Line {
	if len(cols) < 2 || len(lines) == 0 {
		return lines
	}

	// Gutter midpoints are the candidate cut positions.
	cuts := make([]float64, 0, len(cols)-1)
	for i := 0; i+1 < len(cols); i++ {
		cuts = append(cuts, (cols[i].MaxX+cols[i+1].MinX)/2)
	}

	out := make([]Line, 0, len(lines))
	for _, l := range lines {
		out = append(out, splitLineAtCuts(cfg, l, cuts, fonts)...)
	}
	sortLines(out)
	return out
}

// splitLineAtCuts divides one line wherever a real inter-glyph gap contains a
// cut position.
func splitLineAtCuts(cfg Config, l Line, cuts []float64, fonts []*pdf.Font) []Line {
	if len(l.Glyphs) < 2 {
		return []Line{l}
	}

	medAdvance := medianAdvance(l.Glyphs)

	// Find glyph indices after which the line should break.
	var breaks []int
	for i := 0; i+1 < len(l.Glyphs); i++ {
		prev, next := l.Glyphs[i], l.Glyphs[i+1]
		gapStart := prev.X + prev.Advance
		gapEnd := next.X
		if gapEnd <= gapStart {
			continue
		}
		// The gap must be at least a space wide to count as a column break;
		// ordinary word spacing never spans a gutter.
		if gapEnd-gapStart < spaceThreshold(cfg, prev, fonts, medAdvance)*2 {
			continue
		}
		for _, cut := range cuts {
			if cut > gapStart && cut < gapEnd {
				breaks = append(breaks, i)
				break
			}
		}
	}
	if len(breaks) == 0 {
		return []Line{l}
	}

	var out []Line
	start := 0
	for _, b := range append(breaks, len(l.Glyphs)-1) {
		seg := l.Glyphs[start : b+1]
		start = b + 1
		if nl, ok := buildLine(cfg, seg, fonts); ok {
			out = append(out, nl)
		}
	}
	if len(out) == 0 {
		return []Line{l}
	}
	return out
}

// OrderLines puts lines into reading order across columns.
//
// Full-width lines act as barriers: they divide the page into horizontal
// bands, and within each band the columns are read left to right, each top to
// bottom. That is what keeps a spanning headline or figure caption in its
// correct place rather than buried inside one column.
func OrderLines(lines []Line, cols []Column) []Line {
	if len(cols) < 2 || len(lines) == 0 {
		out := make([]Line, len(lines))
		copy(out, lines)
		sortLines(out)
		return out
	}

	sorted := make([]Line, len(lines))
	copy(sorted, lines)
	sortLines(sorted)

	out := make([]Line, 0, len(sorted))
	band := make([]Line, 0, len(sorted))

	flush := func() {
		if len(band) == 0 {
			return
		}
		for ci := range cols {
			for _, l := range band {
				if columnOf(cols, l) == ci {
					out = append(out, l)
				}
			}
		}
		band = band[:0]
	}

	for _, l := range sorted {
		if columnOf(cols, l) < 0 {
			// Full-width: close the band, emit it, then the barrier itself.
			flush()
			out = append(out, l)
			continue
		}
		band = append(band, l)
	}
	flush()
	return out
}

// columnOf returns the index of the column a line belongs to, or -1 when the
// line spans a gutter and is therefore full-width.
func columnOf(cols []Column, l Line) int {
	if len(cols) < 2 {
		return 0
	}
	for i := 0; i+1 < len(cols); i++ {
		mid := (cols[i].MaxX + cols[i+1].MinX) / 2
		if l.Bounds.MinX < mid && l.Bounds.MaxX > mid {
			return -1
		}
	}
	return columnIndexOf(cols, (l.Bounds.MinX+l.Bounds.MaxX)/2)
}
