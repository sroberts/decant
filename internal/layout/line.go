package layout

import (
	"math"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/sroberts/decant/internal/pdf"
)

// Line is a run of glyphs sharing a baseline, in reading order left to right.
type Line struct {
	// Text is the assembled string, with spaces inserted at gaps, ligatures
	// decomposed, soft hyphens stripped, and NFC applied.
	Text string

	// Baseline is the y coordinate the glyphs cluster around, in page space.
	Baseline float64
	// Bounds is the union of the glyph advance boxes.
	Bounds pdf.Rect
	// Size is the median effective font size across the line.
	Size float64
	// Font is the line's dominant font, or nil when unresolved.
	Font *pdf.Font

	// Glyphs are the source glyphs, retained through stage 6 and released
	// afterward per the memory budget in spec section 9.
	Glyphs []pdf.Glyph

	// Rotation is the median baseline angle in degrees.
	Rotation float64
}

// Indent returns the line's left edge, which paragraph detection compares
// against the block median.
func (l *Line) Indent() float64 { return l.Bounds.MinX }

// Width returns the horizontal extent.
func (l *Line) Width() float64 { return l.Bounds.Width() }

// PageLines is the result of assembling one page.
type PageLines struct {
	Lines []Line
	// Rotated holds runs whose baseline angle exceeded the tolerance. They are
	// dropped from Lines unless Config.KeepRotated is set.
	Rotated []Line
	// DecodeFailures counts glyphs that mapped to U+FFFD.
	DecodeFailures int
	// GlyphCount is the number of glyphs considered, after invisible-text
	// filtering.
	GlyphCount int
	// UsedInvisibleText records that the page had no visible glyphs and the
	// mode-3 layer was kept instead, which is the searchable-scan case in
	// spec section 4.2.
	UsedInvisibleText bool
}

// AssembleLines clusters a page's glyphs into lines.
//
// Invisible text (Tr 3 and Tr 7) drops, except when the page carries no
// visible glyphs at all: that is the OCR layer of a searchable scan and the
// only text available.
func AssembleLines(cfg Config, pc *pdf.PageContent) *PageLines {
	out := &PageLines{}
	if pc == nil || len(pc.Glyphs) == 0 {
		return out
	}

	glyphs := filterVisible(pc.Glyphs)
	if len(glyphs) == 0 {
		glyphs = pc.Glyphs
		out.UsedInvisibleText = true
	}
	out.GlyphCount = len(glyphs)

	// Separate rotated runs before clustering; their baselines run along a
	// different axis and would corrupt the vertical grouping.
	upright, rotated := splitByRotation(glyphs, cfg.RotationTolerance)

	out.Lines = clusterLines(cfg, upright, pc.Fonts)
	if len(rotated) > 0 {
		out.Rotated = clusterLines(cfg, rotated, pc.Fonts)
		if cfg.KeepRotated {
			out.Lines = append(out.Lines, out.Rotated...)
			sortLines(out.Lines)
		}
	}

	for _, g := range glyphs {
		if g.Missing {
			out.DecodeFailures++
		}
	}
	return out
}

func filterVisible(gs []pdf.Glyph) []pdf.Glyph {
	n := 0
	for _, g := range gs {
		if g.Visible() {
			n++
		}
	}
	if n == len(gs) {
		return gs
	}
	if n == 0 {
		return nil
	}
	out := make([]pdf.Glyph, 0, n)
	for _, g := range gs {
		if g.Visible() {
			out = append(out, g)
		}
	}
	return out
}

func splitByRotation(gs []pdf.Glyph, tol float64) (upright, rotated []pdf.Glyph) {
	anyRotated := false
	for _, g := range gs {
		if math.Abs(g.Rotation) > tol {
			anyRotated = true
			break
		}
	}
	if !anyRotated {
		return gs, nil
	}
	upright = make([]pdf.Glyph, 0, len(gs))
	for _, g := range gs {
		if math.Abs(g.Rotation) > tol {
			rotated = append(rotated, g)
		} else {
			upright = append(upright, g)
		}
	}
	return upright, rotated
}

// clusterLines groups glyphs into lines by baseline proximity.
func clusterLines(cfg Config, gs []pdf.Glyph, fonts []*pdf.Font) []Line {
	if len(gs) == 0 {
		return nil
	}

	medianHeight := medianGlyphSize(gs)
	tol := cfg.BaselineTolerance * medianHeight
	if tol <= 0 {
		tol = 1
	}

	// Sort by baseline, then by x within a baseline. Sorting up front makes
	// clustering a single pass.
	idx := make([]int, len(gs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ga, gb := gs[idx[a]], gs[idx[b]]
		if ga.Y != gb.Y {
			return ga.Y < gb.Y
		}
		return ga.X < gb.X
	})

	var lines []Line
	var cur []pdf.Glyph
	// ref is the baseline the running cluster is measured against. Using the
	// cluster's first glyph rather than a running mean keeps a gently sloping
	// line from drifting into the line below.
	var ref float64

	flush := func() {
		if len(cur) == 0 {
			return
		}
		if l, ok := buildLine(cfg, cur, fonts); ok {
			lines = append(lines, l)
		}
		cur = nil
	}

	for _, i := range idx {
		g := gs[i]
		if len(cur) == 0 {
			ref = g.Y
			cur = append(cur, g)
			continue
		}
		if math.Abs(g.Y-ref) <= tol {
			cur = append(cur, g)
			continue
		}
		flush()
		ref = g.Y
		cur = append(cur, g)
	}
	flush()

	sortLines(lines)
	return lines
}

func sortLines(lines []Line) {
	sort.SliceStable(lines, func(a, b int) bool {
		if lines[a].Baseline != lines[b].Baseline {
			return lines[a].Baseline < lines[b].Baseline
		}
		return lines[a].Bounds.MinX < lines[b].Bounds.MinX
	})
}

// buildLine assembles one line's text and metrics from its glyphs.
func buildLine(cfg Config, gs []pdf.Glyph, fonts []*pdf.Font) (Line, bool) {
	if len(gs) == 0 {
		return Line{}, false
	}
	sort.SliceStable(gs, func(a, b int) bool { return gs[a].X < gs[b].X })

	l := Line{Glyphs: gs}
	l.Size = medianGlyphSize(gs)
	l.Baseline = medianBaseline(gs)
	l.Rotation = gs[0].Rotation
	l.Font = dominantFont(gs, fonts)

	// The advance-weighted median gap fallback for space detection, used when
	// the font declares no space glyph.
	medAdvance := medianAdvance(gs)

	var sb strings.Builder
	sb.Grow(len(gs) + 8)

	for i, g := range gs {
		if i > 0 {
			prev := gs[i-1]
			gap := g.X - (prev.X + prev.Advance)
			if gap > spaceThreshold(cfg, prev, fonts, medAdvance) {
				sb.WriteRune(' ')
			}
		}
		sb.WriteRune(g.Rune)

		// Advance box, used for the line bounds.
		x0, x1 := g.X, g.X+g.Advance
		if x1 < x0 {
			x0, x1 = x1, x0
		}
		// Approximate the glyph box vertically from the font size: ascent
		// above the baseline, a quarter em of descent below.
		top := g.Y - g.Size*0.75 - g.Rise
		bot := g.Y + g.Size*0.25 - g.Rise
		l.Bounds = l.Bounds.Union(pdf.Rect{MinX: x0, MinY: top, MaxX: x1, MaxY: bot})
	}

	l.Text = normalizeText(sb.String())
	if strings.TrimSpace(l.Text) == "" {
		return Line{}, false
	}
	return l, true
}

// spaceThreshold returns the gap width, in page space, above which a space is
// inserted after glyph g.
func spaceThreshold(cfg Config, g pdf.Glyph, fonts []*pdf.Font, medAdvance float64) float64 {
	if f := fontOf(g, fonts); f != nil {
		if sw, ok := f.SpaceWidth(); ok && sw > 0 {
			return cfg.SpaceGapRatio * (sw / 1000 * g.Size)
		}
	}
	if medAdvance > 0 {
		return cfg.SpaceGapRatio * medAdvance
	}
	// Neither a space glyph nor a usable advance. Half an em is a safe
	// conservative gate that under-inserts rather than shredding words.
	return 0.5 * g.Size
}

func fontOf(g pdf.Glyph, fonts []*pdf.Font) *pdf.Font {
	if g.FontID == pdf.NoFont || int(g.FontID) >= len(fonts) {
		return nil
	}
	return fonts[g.FontID]
}

// dominantFont returns the font covering the most glyphs on the line.
func dominantFont(gs []pdf.Glyph, fonts []*pdf.Font) *pdf.Font {
	counts := map[pdf.FontID]int{}
	for _, g := range gs {
		counts[g.FontID]++
	}
	best, bestN := pdf.NoFont, 0
	for id, n := range counts {
		// Ties break toward the lower ID so the result is deterministic.
		if n > bestN || (n == bestN && id < best) {
			best, bestN = id, n
		}
	}
	if best == pdf.NoFont || int(best) >= len(fonts) {
		return nil
	}
	return fonts[best]
}

// ligatures decomposes the Latin presentation forms that PDF fonts emit.
// NFC alone leaves these intact, and NFKC would fold far more than intended.
var ligatures = strings.NewReplacer(
	"ﬀ", "ff",
	"ﬁ", "fi",
	"ﬂ", "fl",
	"ﬃ", "ffi",
	"ﬄ", "ffl",
	"ﬅ", "st",
	"ﬆ", "st",
	"\u00ad", "", // soft hyphen
	"\ufeff", "", // zero-width no-break space (byte order mark)
	"\u200b", "", // zero-width space
)

// normalizeText applies the text normalization from spec section 4.2.
func normalizeText(s string) string {
	s = ligatures.Replace(s)
	s = norm.NFC.String(s)
	// Collapse runs of spaces introduced by wide inter-glyph gaps.
	if strings.Contains(s, "  ") {
		s = strings.Join(strings.Fields(s), " ")
	}
	return strings.TrimRight(s, " ")
}

// --- statistics helpers ---

func medianGlyphSize(gs []pdf.Glyph) float64 {
	if len(gs) == 0 {
		return 0
	}
	v := make([]float64, 0, len(gs))
	for _, g := range gs {
		if g.Size > 0 {
			v = append(v, g.Size)
		}
	}
	return median(v)
}

func medianBaseline(gs []pdf.Glyph) float64 {
	v := make([]float64, 0, len(gs))
	for _, g := range gs {
		v = append(v, g.Y)
	}
	return median(v)
}

func medianAdvance(gs []pdf.Glyph) float64 {
	v := make([]float64, 0, len(gs))
	for _, g := range gs {
		if g.Advance > 0 {
			v = append(v, g.Advance)
		}
	}
	return median(v)
}

// median returns the middle value, or 0 for an empty slice. It sorts a copy so
// callers can pass slices they still need.
func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := make([]float64, len(v))
	copy(c, v)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}
