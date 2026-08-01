package layout

import "github.com/sroberts/decant/internal/pdf"

// PageLayout is the result of running stages 3 through 5 on one page.
type PageLayout struct {
	// Columns is the detected column geometry, always at least one entry for
	// a page carrying text.
	Columns []Column
	// Blocks are in reading order: column by column within each horizontal
	// band, with full-width elements as band barriers.
	Blocks []Block
	// Lines are the assembled lines in the same reading order.
	Lines []Line

	// DecodeFailures counts glyphs that mapped to U+FFFD.
	DecodeFailures int
	// GlyphCount is the number of glyphs considered after invisible-text
	// filtering.
	GlyphCount int
	// RotatedDropped counts rotated runs discarded.
	RotatedDropped int
	// UsedInvisibleText records that the page had no visible glyphs and the
	// mode-3 layer was kept instead.
	UsedInvisibleText bool
}

// AnalyzePage runs column detection, line assembly, and block segmentation
// for one page, returning content in reading order.
//
// Column detection runs before line assembly is finalized because a line
// clustered purely by baseline can span two columns. Splitting those at the
// gutter, rather than never merging across it, is what lets a full-width
// heading stay intact: it has no inter-glyph gap at the gutter to split on.
func AnalyzePage(cfg Config, pc *pdf.PageContent) *PageLayout {
	out := &PageLayout{}
	if pc == nil || len(pc.Glyphs) == 0 {
		return out
	}

	pl := AssembleLines(cfg, pc)
	out.DecodeFailures = pl.DecodeFailures
	out.GlyphCount = pl.GlyphCount
	out.UsedInvisibleText = pl.UsedInvisibleText
	if !cfg.KeepRotated {
		out.RotatedDropped = len(pl.Rotated)
	}
	if len(pl.Lines) == 0 {
		return out
	}

	// Detect columns from the glyphs that survived filtering, which is the
	// same set the lines were built from.
	glyphs := make([]pdf.Glyph, 0, out.GlyphCount)
	for _, l := range pl.Lines {
		glyphs = append(glyphs, l.Glyphs...)
	}

	out.Columns = DetectColumns(cfg, glyphs, pc.Fonts)
	lines := SplitLinesAtGutters(cfg, pl.Lines, out.Columns, pc.Fonts)
	out.Lines = OrderLines(lines, out.Columns)
	out.Blocks = segmentBlocks(cfg, out.Lines, out.Columns)
	return out
}
