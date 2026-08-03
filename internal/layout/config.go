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

	// ColumnMinRows is the number of text-carrying rows a page must have
	// before its projection profile is trusted at all.
	//
	// Also not in the spec, and the guard that matters most in practice. The
	// profile asks whether a band is empty across 60% of rows; with four rows
	// that question is meaningless, so title pages, part openers, and figure
	// pages reliably produce phantom gutters.
	ColumnMinRows int

	// ColumnMinLines is the number of assembled lines each detected column
	// must contain for the split to survive.
	//
	// Checked after lines are split at the gutters, which is stronger
	// evidence than the glyph share: a real column carries many lines, while
	// a ragged right edge or a bulleted list carries none on one side.
	ColumnMinLines int

	// --- images, spec 4.7 ---

	// BackgroundCoverRatio is the share of the page an image must cover, when
	// painted beneath the text, to be dropped as a background or watermark.
	// Spec 4.7: 0.95.
	BackgroundCoverRatio float64

	// MinImagePoints is the smallest edge, in points, an image may have
	// before it is dropped. Spec 4.7 states 16 pixels; placement is measured
	// in points, which at the 72 dpi of PDF user space is the same number.
	MinImagePoints float64

	// MinImageAreaRatio is the share of page area below which an image is
	// dropped. Spec 4.7: 0.02.
	MinImageAreaRatio float64

	// KeepSmallImages retains images the size rules would drop. Spec 3:
	// --keep-small-images.
	KeepSmallImages bool

	// KeepInlineImages places inline (BI) images. They are recorded but not
	// extracted, so this is off; the flag exists to make that visible rather
	// than silent.
	KeepInlineImages bool

	// InlineImageWidthRatio is the fraction of the text column width below
	// which an image inside a paragraph flows inline. Spec 4.7: 0.4.
	InlineImageWidthRatio float64

	// CaptionGapLines is how many line heights a caption may sit from its
	// figure. Spec 4.6: 1.5.
	CaptionGapLines float64

	// CaptionSizeRatio is how far below the body font a caption is set.
	// Spec 4.6 says "size below body"; 0.05 gives a small tolerance so
	// rounding does not defeat the test.
	CaptionSizeRatio float64

	// CaptionOverlapRatio is the horizontal overlap a block must share with
	// an image to be considered its caption. Not in the spec: without it a
	// sidebar level with a figure binds to it as a caption.
	CaptionOverlapRatio float64

	// --- structure, spec 4.6 ---

	// Dehyphenator decides line-break hyphens. Nil disables dehyphenation,
	// which is what --no-dehyphenate and an unsupported language both do.
	Dehyphenator Dehyphenator

	// QuoteIndentEm is how far both margins must be inset beyond the block
	// median, in em, for a blockquote. Spec 4.6: 1.5.
	QuoteIndentEm float64

	// FootnoteBandRatio is the fraction of page height, measured from the
	// bottom, in which a footnote may sit. Spec 4.6: 0.2.
	FootnoteBandRatio float64

	// FootnoteSizeRatio is how far below the body font a footnote is set.
	// Spec 4.6: 0.1.
	FootnoteSizeRatio float64

	// SuperscriptRiseEm is the baseline offset above which a glyph counts as
	// a superscript, as a fraction of em. Spec 4.6: 0.2.
	SuperscriptRiseEm float64

	// SuperscriptSizeRatio is how much smaller than its line a superscript
	// glyph is. Spec 4.6 says "reduced size"; 0.85 is the ratio below which
	// a glyph qualifies.
	SuperscriptSizeRatio float64

	// --- furniture, spec 4.5 ---

	// FurnitureBandRatio is the fraction of page height at the top and bottom
	// in which running heads and folios live. Spec 4.5: 0.08.
	FurnitureBandRatio float64

	// FurnitureRepeatRatio is the fraction of sampled pages a block's text
	// must repeat on to be removed. Spec 4.5: 0.6.
	FurnitureRepeatRatio float64

	// FurnitureSamplePages is how many pages the sampler examines.
	// Spec 4.5: 20.
	FurnitureSamplePages int

	// FurnitureMinPages is the document length below which furniture removal
	// is skipped entirely. Spec 4.5: 5.
	FurnitureMinPages int

	// KeepHeaders retains running heads and folios. Spec 3: --keep-headers.
	KeepHeaders bool

	// ListMarker reports whether a line opens with a list marker.
	//
	// Paragraph reconstruction needs it: spec 4.6 sets list items with a
	// hanging indent, and the indent rule would otherwise split every item
	// between its first line and its continuation. Nil disables the check.
	ListMarker func(string) bool

	// --- tables, spec 4.8 ---

	// TableMode selects how detected tables are emitted.
	TableMode TableMode

	// RuleMaxThickness is the stroke width above which a painted segment is a
	// bar rather than a ruling line. Spec 4.8: 2 pt.
	RuleMaxThickness float64

	// RuleClusterTolerance is how far apart two rules may sit and still count
	// as the same boundary. Spec 4.8 gives 2 pt for column alignment; the
	// same figure serves here, since a boundary drawn as several segments is
	// rarely off by more.
	RuleClusterTolerance float64

	// RuleRowCoverRatio is the fraction of a row's height a vertical rule
	// must span to separate its cells. Below it the boundary is absent and
	// the cells merge into a colspan.
	RuleRowCoverRatio float64

	// TableRegionGap is the vertical gap between rules beyond which they
	// belong to separate tables.
	TableRegionGap float64

	// TableColumnTolerance is how close two column starts must be to count as
	// the same boundary. Spec 4.8: 2 pt.
	TableColumnTolerance float64

	// TableMinSharedColumns is how many boundaries every row must share for
	// the alignment signal to fire. Spec 4.8: 2.
	TableMinSharedColumns int

	// TableMinRows is the number of consecutive tabulated lines the alignment
	// signal needs. Spec 4.8: 3.
	TableMinRows int

	// TableMinFilledRatio is the fraction of a ruled grid's cells that must
	// carry text for it to be a table.
	//
	// Not in the spec, and the guard the ruling signal most needs: diagrams
	// draw axis-aligned lines that form apparent grids, and without this a
	// mathematics textbook yields seventeen phantom tables, some of which
	// shred a figure's caption into cells.
	TableMinFilledRatio float64
}

// TableMode selects how a detected table is emitted, per spec 4.8.
type TableMode string

const (
	// TableAuto picks by confidence: a real table at high, space-preserved
	// text otherwise.
	TableAuto TableMode = "auto"
	// TableHTML always emits a table.
	TableHTML TableMode = "html"
	// TableText emits space-preserved text.
	TableText TableMode = "text"
	// TableDrop discards detected tables.
	TableDrop TableMode = "drop"
)

// Dehyphenator decides whether a line-break hyphen is a typesetting artifact
// or part of the word. Spec 4.6 inverts Liang's algorithm to answer it.
type Dehyphenator interface {
	// JoinFragments reports whether the hyphen between two fragments should
	// be dropped, along with a short reason for the conversion report.
	JoinFragments(left, right string) (drop bool, reason string)
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
		ColumnMinRows:        8,
		ColumnMinLines:       3,

		BackgroundCoverRatio:  0.95,
		MinImagePoints:        16,
		MinImageAreaRatio:     0.02,
		InlineImageWidthRatio: 0.4,
		CaptionGapLines:       1.5,
		CaptionSizeRatio:      0.05,
		CaptionOverlapRatio:   0.3,

		QuoteIndentEm:        1.5,
		FootnoteBandRatio:    0.2,
		FootnoteSizeRatio:    0.1,
		SuperscriptRiseEm:    0.2,
		SuperscriptSizeRatio: 0.85,

		FurnitureBandRatio:   0.08,
		FurnitureRepeatRatio: 0.6,
		FurnitureSamplePages: 20,
		FurnitureMinPages:    5,

		TableMode:             TableAuto,
		RuleMaxThickness:      2,
		RuleClusterTolerance:  2,
		RuleRowCoverRatio:     0.6,
		TableRegionGap:        36,
		TableColumnTolerance:  2,
		TableMinSharedColumns: 2,
		TableMinRows:          3,
		TableMinFilledRatio:   0.5,
	}
}
