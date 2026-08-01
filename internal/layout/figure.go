package layout

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/sroberts/decant/internal/pdf"
)

// Figure is an image placed in reading order, with the caption bound to it.
type Figure struct {
	// Draw is the source image placement.
	Draw pdf.ImageDraw
	// Bounds is the placement rectangle in page space.
	Bounds pdf.Rect
	// Caption is the caption text, empty when none was found.
	Caption string
	// Inline marks an image narrow enough, and positioned inside a paragraph,
	// to belong in the text flow rather than as a block-level figure.
	Inline bool
	// Column is the index of the column the figure sits in, or -1 when it
	// spans the gutters. Placement in reading order needs it: a figure
	// belongs inside its own column's run, not at whatever vertical position
	// it happens to share with the other column.
	Column int
}

// captionPrefix matches the labels a caption conventionally opens with. Spec
// section 4.6 names Figure, Table, Fig., and Plate followed by a number.
var captionPrefix = regexp.MustCompile(`^\s*(Figure|Fig\.|Table|Plate|Abbildung|Tabelle|Abb\.)\s*[0-9IVXivx]`)

// PlaceFigures decides which images survive, where they sit in reading order,
// and which blocks are their captions.
//
// It returns the surviving figures and the set of block indices consumed as
// captions, so the caller can avoid emitting those blocks twice.
func PlaceFigures(cfg Config, pl *PageLayout, draws []pdf.ImageDraw, pageW, pageH float64, bodySize float64) ([]Figure, map[int]bool) {
	kept := filterImages(cfg, draws, pageW, pageH, len(pl.Lines) > 0)
	if len(kept) == 0 {
		return nil, nil
	}

	figs := make([]Figure, 0, len(kept))
	for _, d := range kept {
		figs = append(figs, Figure{
			Draw:   d,
			Bounds: d.Rect,
			Column: ColumnOfRect(pl.Columns, d.Rect),
		})
	}

	captions := map[int]bool{}
	assignCaptions(cfg, figs, pl.Blocks, bodySize, captions)
	markInline(cfg, figs, pl, pageW)

	sort.SliceStable(figs, func(i, j int) bool {
		if figs[i].Bounds.MinY != figs[j].Bounds.MinY {
			return figs[i].Bounds.MinY < figs[j].Bounds.MinY
		}
		if figs[i].Bounds.MinX != figs[j].Bounds.MinX {
			return figs[i].Bounds.MinX < figs[j].Bounds.MinX
		}
		return figs[i].Draw.Order < figs[j].Draw.Order
	})
	return figs, captions
}

// filterImages applies the drop rules from spec section 4.7.
func filterImages(cfg Config, draws []pdf.ImageDraw, pageW, pageH float64, pageHasText bool) []pdf.ImageDraw {
	if len(draws) == 0 {
		return nil
	}
	pageArea := pageW * pageH
	out := make([]pdf.ImageDraw, 0, len(draws))

	for _, d := range draws {
		if d.Inline && !cfg.KeepInlineImages {
			// An inline image's bytes are not extracted, so it has nothing to
			// place. Its position is still recorded for scan coverage.
			continue
		}
		w, h := d.Width(), d.Height()
		if w <= 0 || h <= 0 {
			continue
		}
		area := w * h

		// Backgrounds and watermarks: page-covering and painted before any
		// text on a page that has text.
		if pageArea > 0 && area/pageArea > cfg.BackgroundCoverRatio &&
			d.GlyphsBefore == 0 && pageHasText {
			continue
		}

		if !cfg.KeepSmallImages {
			// Small in absolute terms, or a negligible share of the page.
			if w < cfg.MinImagePoints || h < cfg.MinImagePoints {
				continue
			}
			if pageArea > 0 && area/pageArea < cfg.MinImageAreaRatio {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// assignCaptions binds nearby blocks to figures.
//
// Spec section 4.6 makes a caption a block within 1.5 line heights of an
// image, set below the body size, or opening with a Figure/Table/Plate label.
// A label match alone is enough: captions are routinely set at body size, and
// the label is the stronger signal.
func assignCaptions(cfg Config, figs []Figure, blocks []Block, bodySize float64, consumed map[int]bool) {
	if len(blocks) == 0 {
		return
	}
	for i := range figs {
		best, bestDist := -1, math.Inf(1)

		for bi := range blocks {
			if consumed[bi] {
				continue
			}
			b := blocks[bi]
			if len(b.Lines) == 0 {
				continue
			}
			text := blockText(b)
			if strings.TrimSpace(text) == "" {
				continue
			}

			// Horizontal association: a caption sits under or over its
			// figure, not off to the side.
			if horizontalOverlap(b.Bounds, figs[i].Bounds) < cfg.CaptionOverlapRatio {
				continue
			}

			lineH := b.Lines[0].Size
			if lineH <= 0 {
				lineH = bodySize
			}
			gap := verticalGap(figs[i].Bounds, b.Bounds)
			near := gap <= cfg.CaptionGapLines*lineH
			labelled := captionPrefix.MatchString(text)
			smaller := bodySize > 0 && b.Lines[0].Size < bodySize*(1-cfg.CaptionSizeRatio)

			if !labelled && !(near && smaller) {
				continue
			}
			// A labelled caption may sit slightly further away, but not on
			// the other side of the page.
			if labelled && gap > cfg.CaptionGapLines*lineH*3 {
				continue
			}
			if gap < bestDist {
				best, bestDist = bi, gap
			}
		}

		if best >= 0 {
			figs[i].Caption = blockText(blocks[best])
			consumed[best] = true
		}
	}
}

// markInline flags images that belong in the text flow.
//
// Spec section 4.7: narrower than 40% of the text column and vertically
// inside a paragraph. Both conditions matter, since a narrow image sitting
// between paragraphs is still a block-level figure.
func markInline(cfg Config, figs []Figure, pl *PageLayout, pageW float64) {
	columnWidth := pageW
	if len(pl.Columns) > 0 {
		columnWidth = pl.Columns[0].Width()
		for _, c := range pl.Columns[1:] {
			if w := c.Width(); w < columnWidth {
				columnWidth = w
			}
		}
	}
	if columnWidth <= 0 {
		return
	}

	for i := range figs {
		if figs[i].Caption != "" {
			// A captioned image is a figure by definition.
			continue
		}
		if figs[i].Bounds.Width() >= cfg.InlineImageWidthRatio*columnWidth {
			continue
		}
		for _, b := range pl.Blocks {
			if len(b.Lines) < 2 {
				continue
			}
			if figs[i].Bounds.MinY > b.Bounds.MinY && figs[i].Bounds.MaxY < b.Bounds.MaxY {
				figs[i].Inline = true
				break
			}
		}
	}
}

// ColumnOfRect returns the index of the column a rectangle sits in, or -1
// when it spans a gutter and is therefore full-width.
func ColumnOfRect(cols []Column, r pdf.Rect) int {
	if len(cols) < 2 {
		return 0
	}
	for i := 0; i+1 < len(cols); i++ {
		mid := (cols[i].MaxX + cols[i+1].MinX) / 2
		if r.MinX < mid && r.MaxX > mid {
			return -1
		}
	}
	return columnIndexOf(cols, (r.MinX+r.MaxX)/2)
}

// verticalGap returns the vertical distance between two boxes, and 0 when
// they overlap.
func verticalGap(a, b pdf.Rect) float64 {
	if b.MinY >= a.MaxY {
		return b.MinY - a.MaxY
	}
	if a.MinY >= b.MaxY {
		return a.MinY - b.MaxY
	}
	return 0
}

// blockText joins a block's lines the way paragraph reconstruction would.
func blockText(b Block) string {
	parts := make([]string, 0, len(b.Lines))
	for _, l := range b.Lines {
		if s := strings.TrimSpace(l.Text); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}
