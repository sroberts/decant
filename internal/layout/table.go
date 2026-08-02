package layout

import (
	"math"
	"sort"
	"strings"

	"github.com/sroberts/decant/internal/pdf"
)

// Confidence ranks how sure table detection is, which drives what
// --table-mode=auto emits per spec section 4.8.
type Confidence string

const (
	// ConfidenceHigh means both signals fired: a ruling grid and aligned
	// columns. Spec 4.8 emits a real <table> only here.
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium means a ruling grid with no corroborating alignment.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow means aligned columns with no rulings.
	ConfidenceLow Confidence = "low"
)

// Cell is one table cell.
type Cell struct {
	Text string
	// ColSpan is 1 unless the cell spans columns, which happens where a
	// vertical rule is absent between two boundaries.
	ColSpan int
	// Bounds is the cell's region in page space.
	Bounds pdf.Rect
}

// Table is a detected table.
type Table struct {
	Bounds pdf.Rect
	Rows   [][]Cell
	// Confidence is what --table-mode=auto keys on.
	Confidence Confidence
	// Rulings and Aligned record which signals fired, for the report.
	Rulings bool
	Aligned bool
	// LineIndices are the indices of the page's lines consumed by this table,
	// so the caller does not emit them twice.
	LineIndices map[int]bool
}

// ColumnCount returns the widest row's column count, counting spans.
func (t Table) ColumnCount() int {
	best := 0
	for _, row := range t.Rows {
		n := 0
		for _, c := range row {
			n += c.ColSpan
		}
		if n > best {
			best = n
		}
	}
	return best
}

// DetectTables finds tables on a page, per spec section 4.8.
//
// Two signals are sought independently. Ruling lines give a grid directly.
// Alignment gives column boundaries where a table is set without rules, which
// is common in scientific typesetting. Both firing is high confidence; either
// alone is weaker, and section 4.8 emits different markup for each.
func DetectTables(cfg Config, pl *PageLayout, rules []pdf.Rule) []Table {
	if cfg.TableMode == TableDrop || len(pl.Lines) == 0 {
		return nil
	}

	var out []Table
	consumed := map[int]bool{}

	for _, g := range findGrids(cfg, rules) {
		t, ok := buildTableFromGrid(cfg, pl, g, consumed)
		if !ok {
			continue
		}
		out = append(out, t)
		for i := range t.LineIndices {
			consumed[i] = true
		}
	}

	// Alignment-only tables, over the lines no ruling grid claimed.
	for _, t := range findAlignedTables(cfg, pl, consumed) {
		out = append(out, t)
		for i := range t.LineIndices {
			consumed[i] = true
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Bounds.MinY < out[j].Bounds.MinY
	})
	return out
}

// grid is a candidate ruling structure: the distinct boundary positions and
// the rules that produced them.
type grid struct {
	xs, ys []float64
	bounds pdf.Rect
	hRules []pdf.Rule
	vRules []pdf.Rule
}

// findGrids groups ruling lines into candidate table grids.
//
// Rules are clustered by position first, so the several segments a typesetter
// draws along one boundary collapse into a single line, then grouped into
// regions that overlap in both axes.
func findGrids(cfg Config, rules []pdf.Rule) []grid {
	if len(rules) == 0 {
		return nil
	}

	var hs, vs []pdf.Rule
	for _, r := range rules {
		if r.Thickness > cfg.RuleMaxThickness {
			continue
		}
		if r.Horizontal {
			hs = append(hs, r)
		} else {
			vs = append(vs, r)
		}
	}
	// Spec 4.8 wants at least 2 rows by 2 columns, which needs three lines on
	// each axis.
	if len(hs) < 3 || len(vs) < 3 {
		return nil
	}

	// One grid per connected region. Regions are separated by taking the
	// overall extent of the rules and splitting where a vertical gap exceeds
	// the largest row height a table plausibly has.
	regions := splitRuleRegions(cfg, hs, vs)

	var out []grid
	for _, reg := range regions {
		xs := clusterPositions(verticalPositions(reg.vRules), cfg.RuleClusterTolerance)
		ys := clusterPositions(horizontalPositions(reg.hRules), cfg.RuleClusterTolerance)
		if len(xs) < 3 || len(ys) < 3 {
			continue
		}
		reg.xs, reg.ys = xs, ys
		out = append(out, reg)
	}
	return out
}

// splitRuleRegions groups rules into regions that could each be one table.
func splitRuleRegions(cfg Config, hs, vs []pdf.Rule) []grid {
	all := append(append([]pdf.Rule{}, hs...), vs...)
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Bounds.MinY < all[j].Bounds.MinY
	})

	var out []grid
	var cur grid
	started := false

	flush := func() {
		if started && len(cur.hRules) > 0 && len(cur.vRules) > 0 {
			out = append(out, cur)
		}
		cur = grid{}
		started = false
	}

	for _, r := range all {
		if !started {
			cur.bounds = r.Bounds
			started = true
		} else if r.Bounds.MinY-cur.bounds.MaxY > cfg.TableRegionGap {
			flush()
			cur.bounds = r.Bounds
			started = true
		} else {
			cur.bounds = cur.bounds.Union(r.Bounds)
		}
		if r.Horizontal {
			cur.hRules = append(cur.hRules, r)
		} else {
			cur.vRules = append(cur.vRules, r)
		}
	}
	flush()
	return out
}

func horizontalPositions(rs []pdf.Rule) []float64 {
	out := make([]float64, 0, len(rs))
	for _, r := range rs {
		out = append(out, (r.Bounds.MinY+r.Bounds.MaxY)/2)
	}
	return out
}

func verticalPositions(rs []pdf.Rule) []float64 {
	out := make([]float64, 0, len(rs))
	for _, r := range rs {
		out = append(out, (r.Bounds.MinX+r.Bounds.MaxX)/2)
	}
	return out
}

// clusterPositions collapses near-identical coordinates to their mean.
func clusterPositions(v []float64, tol float64) []float64 {
	if len(v) == 0 {
		return nil
	}
	sorted := append([]float64{}, v...)
	sort.Float64s(sorted)

	var out []float64
	start := 0
	for i := 1; i <= len(sorted); i++ {
		if i < len(sorted) && sorted[i]-sorted[i-1] <= tol {
			continue
		}
		sum := 0.0
		for _, x := range sorted[start:i] {
			sum += x
		}
		out = append(out, sum/float64(i-start))
		start = i
	}
	return out
}

// buildTableFromGrid turns a ruling grid into cells and fills them with text.
func buildTableFromGrid(cfg Config, pl *PageLayout, g grid, consumed map[int]bool) (Table, bool) {
	region := pdf.Rect{
		MinX: g.xs[0], MaxX: g.xs[len(g.xs)-1],
		MinY: g.ys[0], MaxY: g.ys[len(g.ys)-1],
	}
	if region.Width() <= 0 || region.Height() <= 0 {
		return Table{}, false
	}

	t := Table{
		Bounds:      region,
		Rulings:     true,
		LineIndices: map[int]bool{},
	}

	for r := 0; r+1 < len(g.ys); r++ {
		top, bottom := g.ys[r], g.ys[r+1]
		var row []Cell

		for c := 0; c+1 < len(g.xs); {
			left := g.xs[c]
			// Extend the cell rightward across every boundary that carries no
			// vertical rule spanning this row, which is what a colspan is.
			span := 1
			for c+span < len(g.xs)-1 &&
				!hasVerticalRule(g.vRules, g.xs[c+span], top, bottom, cfg) {
				span++
			}
			right := g.xs[c+span]

			cell := Cell{
				ColSpan: span,
				Bounds:  pdf.Rect{MinX: left, MinY: top, MaxX: right, MaxY: bottom},
			}
			cell.Text = collectCellText(cfg, pl, cell.Bounds, t.LineIndices)
			row = append(row, cell)
			c += span
		}
		if len(row) > 0 {
			t.Rows = append(t.Rows, row)
		}
	}

	if len(t.Rows) < 2 || t.ColumnCount() < 2 {
		return Table{}, false
	}
	// A grid is only a table if its cells are actually filled.
	//
	// This guard is not in spec section 4.8, and without it the ruling signal
	// is badly over-eager: a mathematics textbook's diagrams draw enough
	// axis-aligned lines to form apparent grids, and on the sample corpus
	// that produced seventeen "tables" per book, several of which shredded a
	// figure's caption into cells. A ruled box holding one stray label is a
	// diagram, a border, or a form field. A table fills most of its cells.
	if filledRatio(t) < cfg.TableMinFilledRatio {
		return Table{}, false
	}
	// And its cells must hold more than single characters.
	//
	// Also not in section 4.8, and aimed at the same over-eagerness from the
	// other direction. A plotted graph's axes and gridlines form a grid whose
	// cells hold tick labels, and a diagram drawn from repeated marks forms
	// one whose cells hold a single glyph each. Both pass a fill test. Neither
	// is a table, and marking them up as one shreds a figure into cells.
	if substantialCellRatio(t) < cfg.TableMinFilledRatio {
		return Table{}, false
	}

	// Drop any line already claimed by an earlier table.
	for i := range consumed {
		delete(t.LineIndices, i)
	}

	t.Aligned = columnsAlign(cfg, t)
	if t.Aligned {
		t.Confidence = ConfidenceHigh
	} else {
		t.Confidence = ConfidenceMedium
	}
	return t, true
}

// hasVerticalRule reports whether a vertical rule sits at x and spans the row
// between top and bottom.
func hasVerticalRule(vs []pdf.Rule, x, top, bottom float64, cfg Config) bool {
	for _, r := range vs {
		cx := (r.Bounds.MinX + r.Bounds.MaxX) / 2
		if math.Abs(cx-x) > cfg.RuleClusterTolerance {
			continue
		}
		// The rule must cover most of the row to separate its cells.
		overlap := math.Min(r.Bounds.MaxY, bottom) - math.Max(r.Bounds.MinY, top)
		if overlap >= (bottom-top)*cfg.RuleRowCoverRatio {
			return true
		}
	}
	return false
}

// collectCellText gathers a cell's text from the glyphs inside it.
//
// Spec section 4.8 assembles cell text "from glyphs bounded by adjacent
// rulings", and the distinction matters: a table row shares one baseline, so
// line assembly quite correctly merges a whole row into a single line. Taking
// text per line would put the entire row in whichever cell held the line's
// centre and leave every other cell empty.
//
// A line is marked consumed when any of its glyphs land in the cell, so the
// caller does not also emit the row as prose.
func collectCellText(cfg Config, pl *PageLayout, box pdf.Rect, used map[int]bool) string {
	var sb strings.Builder
	for i, l := range pl.Lines {
		inside := l.Glyphs[:0:0]
		for _, g := range l.Glyphs {
			cx := g.X + g.Advance/2
			if cx < box.MinX || cx > box.MaxX {
				continue
			}
			// Vertical containment uses the baseline, which sits inside the
			// row's rules even where the glyph box overlaps them.
			if g.Y < box.MinY || g.Y > box.MaxY {
				continue
			}
			inside = append(inside, g)
		}
		if len(inside) == 0 {
			continue
		}
		used[i] = true

		if frag, ok := buildLine(cfg, inside, nil); ok {
			if s := strings.TrimSpace(StripSuperscriptMarks(frag.Text)); s != "" {
				if sb.Len() > 0 {
					sb.WriteByte(' ')
				}
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

// substantialCellRatio is the fraction of filled cells holding more than a
// single character.
func substantialCellRatio(t Table) float64 {
	filled, substantial := 0, 0
	for _, row := range t.Rows {
		for _, c := range row {
			s := strings.TrimSpace(c.Text)
			if s == "" {
				continue
			}
			filled++
			if len([]rune(s)) > 1 {
				substantial++
			}
		}
	}
	if filled == 0 {
		return 0
	}
	return float64(substantial) / float64(filled)
}

// filledRatio is the fraction of a table's cells carrying text.
func filledRatio(t Table) float64 {
	total, filled := 0, 0
	for _, row := range t.Rows {
		for _, c := range row {
			total++
			if strings.TrimSpace(c.Text) != "" {
				filled++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(filled) / float64(total)
}

// columnsAlign checks the second signal from spec section 4.8 against a
// ruling grid: whether the cells actually line up into columns.
//
// Section 4.8 phrases it as consecutive lines sharing column boundaries,
// which is how the signal is measured on a table with no rules. On a ruled
// grid the boundaries are already known, so the question becomes whether the
// content honors them: a column carrying text on row after row is a real
// column, while a grid whose cells are mostly empty is a form or a layout
// frame that happens to be ruled.
func columnsAlign(cfg Config, t Table) bool {
	filled := map[int]int{}
	for _, row := range t.Rows {
		col := 0
		for _, c := range row {
			if strings.TrimSpace(c.Text) != "" {
				filled[col]++
			}
			col += c.ColSpan
		}
	}

	consistent := 0
	for _, n := range filled {
		if n >= cfg.TableMinRows {
			consistent++
		}
	}
	return consistent >= cfg.TableMinSharedColumns
}

// findAlignedTables detects tables set without ruling lines.
//
// Spec section 4.8: three or more consecutive lines sharing at least two
// column boundaries within tolerance. The rows here are the page's own lines
// grouped by baseline, since a ruleless table has nothing else to go on.
func findAlignedTables(cfg Config, pl *PageLayout, consumed map[int]bool) []Table {
	if cfg.TableMinRows < 2 {
		return nil
	}

	// Group line indices into visual rows by baseline.
	type visRow struct {
		idx   []int
		lines []Line
		y     float64
	}
	var rows []visRow
	for i, l := range pl.Lines {
		if consumed[i] {
			continue
		}
		placed := false
		for j := range rows {
			if math.Abs(rows[j].y-l.Baseline) <= l.Size*cfg.BaselineTolerance {
				rows[j].idx = append(rows[j].idx, i)
				rows[j].lines = append(rows[j].lines, l)
				placed = true
				break
			}
		}
		if !placed {
			rows = append(rows, visRow{idx: []int{i}, lines: []Line{l}, y: l.Baseline})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].y < rows[j].y })

	var out []Table
	for start := 0; start < len(rows); {
		// A run of consecutive rows each carrying at least two pieces, which
		// is the shape of a tabulated line.
		end := start
		for end < len(rows) && len(rows[end].lines) >= 2 {
			end++
		}
		if end-start < cfg.TableMinRows {
			start = maxInt(end, start+1)
			continue
		}

		group := rows[start:end]
		asLines := make([][]Line, 0, len(group))
		for _, r := range group {
			asLines = append(asLines, r.lines)
		}
		if sharedBoundaries(cfg, asLines) < cfg.TableMinSharedColumns {
			start = maxInt(end, start+1)
			continue
		}
		// A table sits inside a column; it does not straddle the page's own
		// column layout.
		//
		// Not in section 4.8, but required by section 4.3. On a two-column
		// page every body line in the left column shares a baseline with one
		// in the right, so the visual rows are perfectly aligned and every
		// boundary is shared. That is the shape this pass looks for, and
		// nothing about the text distinguishes it from a two-column table.
		// The page layout does: those pieces live in different columns, and
		// calling them a table would flatten M2's reading order into rows
		// read across instead of down.
		if straddlesColumns(pl.Columns, asLines) {
			start = maxInt(end, start+1)
			continue
		}

		t := Table{Confidence: ConfidenceLow, Aligned: true, LineIndices: map[int]bool{}}
		for _, r := range group {
			var row []Cell
			sorted := append([]Line{}, r.lines...)
			sort.SliceStable(sorted, func(i, j int) bool {
				return sorted[i].Bounds.MinX < sorted[j].Bounds.MinX
			})
			for _, l := range sorted {
				row = append(row, Cell{
					Text:    strings.TrimSpace(StripSuperscriptMarks(l.Text)),
					ColSpan: 1,
					Bounds:  l.Bounds,
				})
				t.Bounds = t.Bounds.Union(l.Bounds)
			}
			for _, i := range r.idx {
				t.LineIndices[i] = true
			}
			t.Rows = append(t.Rows, row)
		}
		out = append(out, t)
		start = end
	}
	return out
}

// straddlesColumns reports whether a candidate's rows span more than one of
// the page's detected columns, or cross a gutter.
func straddlesColumns(cols []Column, rows [][]Line) bool {
	if len(cols) < 2 {
		return false
	}
	seen := -1
	for _, r := range rows {
		for _, l := range r {
			c := columnOf(cols, l)
			if c < 0 {
				// The line crosses a gutter, so it belongs to no column.
				return true
			}
			if seen < 0 {
				seen = c
				continue
			}
			if c != seen {
				return true
			}
		}
	}
	return false
}

// sharedBoundaries counts the column start positions common to every row.
//
// Spec section 4.8 wants three or more consecutive lines sharing at least two
// boundaries within 2 pt. A boundary counts only when every row in the run has
// a piece starting there, which is what distinguishes a table from prose that
// happens to line up once.
func sharedBoundaries(cfg Config, rows [][]Line) int {
	if len(rows) < 2 {
		return 0
	}

	candidates := make([]float64, 0, len(rows[0]))
	for _, l := range rows[0] {
		candidates = append(candidates, l.Bounds.MinX)
	}

	shared := 0
	for _, x := range candidates {
		inAll := true
		for _, row := range rows[1:] {
			found := false
			for _, l := range row {
				if math.Abs(l.Bounds.MinX-x) <= cfg.TableColumnTolerance {
					found = true
					break
				}
			}
			if !found {
				inAll = false
				break
			}
		}
		if inAll {
			shared++
		}
	}
	return shared
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
