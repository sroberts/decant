// Package layout turns positioned glyphs into lines, blocks, and structured
// content. It is stages 3 through 6 of the pipeline in spec section 4.
package layout

// Config carries the tunable thresholds this package needs. The root package
// owns the full public Heuristics struct and converts into this at the
// boundary, which keeps the public API decoupled from internal stage
// organization.
//
// Every field mirrors a documented default in spec section 4. Zero values are
// not usable; construct with DefaultConfig and override.
type Config struct {
	// BaselineTolerance is the fraction of median glyph height within which
	// two glyphs are considered to share a baseline. Spec 4.3: 0.3.
	BaselineTolerance float64

	// SpaceGapRatio is the fraction of the font's space width that a gap must
	// exceed for a space to be inserted between glyphs. Spec 4.3: 0.25.
	SpaceGapRatio float64

	// RotationTolerance is the maximum absolute baseline angle, in degrees,
	// still treated as horizontal text. Spec 4.3: 5.
	RotationTolerance float64

	// KeepRotated retains rotated runs instead of dropping them with a
	// warning. Spec 4.3 drops by default; margin text is rarely body content.
	KeepRotated bool

	// ParagraphGapRatio is how far the vertical gap between two lines must
	// exceed the running leading before a new paragraph starts. Spec 4.6:
	// 0.25, meaning a 25% overshoot.
	ParagraphGapRatio float64

	// ParagraphIndentEm is the indent, in em, above the block median that
	// starts a new paragraph. Spec 4.6: 0.5.
	ParagraphIndentEm float64

	// ShortLineRatio is the fraction of block width below which a line ending
	// in terminal punctuation is treated as a paragraph end. Spec 4.6: 0.8.
	ShortLineRatio float64

	// BlockGapRatio is the multiple of running median leading beyond which a
	// vertical gap breaks the block. Spec 4.4: 1.5.
	BlockGapRatio float64

	// BlockOverlapRatio is the horizontal overlap, as a fraction of the
	// narrower line, required to merge a line into a block. Spec 4.4: 0.5.
	BlockOverlapRatio float64

	// BlockSizeChangeRatio is the relative font size change that breaks a
	// block. Spec 4.4: 0.15.
	BlockSizeChangeRatio float64

	// Columns forces a column count. Zero detects from the projection
	// profile; 1, 2, or 3 override it. Spec 3: --columns.
	Columns int

	// MaxColumns caps automatic detection. Spec 3 offers up to 3.
	MaxColumns int

	// GutterMinWidthSpaces is how many median space widths a whitespace band
	// must span to count as a gutter. Spec 4.4: 2.
	GutterMinWidthSpaces float64

	// GutterMinHeightRatio is the fraction of text-carrying rows a band must
	// be empty across to count as a gutter. Spec 4.4: 0.6.
	GutterMinHeightRatio float64

	// ColumnMinGlyphRatio is the minimum share of the page's glyphs each
	// detected column must hold for the split to be believed.
	//
	// This guard is not in the spec. Section 4.4 notes the heuristic misfires
	// on tables and figures; without it, a hanging indent or a run of centered
	// headings can read as a gutter and shred a single-column page.
	ColumnMinGlyphRatio float64
}

// DefaultConfig returns the documented defaults from spec section 4.
func DefaultConfig() Config {
	return Config{
		BaselineTolerance:    0.3,
		SpaceGapRatio:        0.25,
		RotationTolerance:    5,
		KeepRotated:          false,
		ParagraphGapRatio:    0.25,
		ParagraphIndentEm:    0.5,
		ShortLineRatio:       0.8,
		BlockGapRatio:        1.5,
		BlockOverlapRatio:    0.5,
		BlockSizeChangeRatio: 0.15,
		Columns:              0,
		MaxColumns:           3,
		GutterMinWidthSpaces: 2,
		GutterMinHeightRatio: 0.6,
		ColumnMinGlyphRatio:  0.1,
	}
}
