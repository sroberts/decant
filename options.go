// Package decant reconstructs semantic, reflowable EPUB 3 from fixed-layout
// PDF.
//
// The conversion engine lives here and the decant command is a thin wrapper
// over it. Analyze and Write are separate so a caller can inspect and correct
// the block tree before committing the EPUB.
package decant

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Profile constrains output for a class of reading device. See spec
// section 5.
type Profile string

const (
	// ProfileStandard is the unconstrained default.
	ProfileStandard Profile = "standard"
	// ProfileCrossPoint targets the Xteink X4 running CrossPoint firmware:
	// a 480x800 E Ink panel on an ESP32-C3 with roughly 380 KB of usable RAM.
	ProfileCrossPoint Profile = "crosspoint"
	// ProfileMinimal drops images and stylesheets entirely.
	ProfileMinimal Profile = "minimal"
)

// Valid reports whether p is a known profile.
func (p Profile) Valid() bool {
	switch p {
	case ProfileStandard, ProfileCrossPoint, ProfileMinimal:
		return true
	}
	return false
}

// SplitMode selects where chapter files break.
type SplitMode string

const (
	// SplitAtH1 breaks at top-level headings. This is the default.
	SplitAtH1 SplitMode = "h1"
	// SplitAtH2 breaks at second-level headings.
	SplitAtH2 SplitMode = "h2"
	// SplitAtPage breaks at every source page.
	SplitAtPage SplitMode = "page"
	// SplitAtNone emits one chapter, subject to MaxChunkBytes.
	SplitAtNone SplitMode = "none"
)

// Valid reports whether s is a known split mode.
func (s SplitMode) Valid() bool {
	switch s {
	case SplitAtH1, SplitAtH2, SplitAtPage, SplitAtNone:
		return true
	}
	return false
}

// Metadata overrides Dublin Core values otherwise taken from the PDF.
type Metadata struct {
	Title    string
	Author   string
	Language string
}

// Heuristics holds every tunable threshold from spec section 4. Each field
// documents its default; DefaultHeuristics returns them all.
//
// Spec principle 5 requires these to be inspectable as well as tunable, which
// is what the probe subcommand dumps.
type Heuristics struct {
	// BaselineTolerance is the fraction of median glyph height within which
	// glyphs share a baseline. Default 0.3.
	BaselineTolerance float64

	// SpaceGapRatio is the fraction of a font's space width that an
	// inter-glyph gap must exceed to become a space. Default 0.25.
	SpaceGapRatio float64

	// RotationTolerance is the maximum baseline angle in degrees still
	// treated as horizontal text. Default 5.
	RotationTolerance float64

	// KeepRotated retains rotated runs rather than dropping them with a
	// warning. Default false.
	KeepRotated bool

	// BlockOverlapRatio is the horizontal overlap, as a fraction of the
	// narrower line, required to merge a line into a block. Default 0.5.
	BlockOverlapRatio float64

	// BlockGapRatio is the multiple of running median leading beyond which a
	// vertical gap breaks a block. Default 1.5.
	BlockGapRatio float64

	// BlockSizeChangeRatio is the relative font size change that breaks a
	// block. Default 0.15.
	BlockSizeChangeRatio float64

	// ParagraphGapRatio is the fractional overshoot of the running leading
	// that starts a new paragraph. Default 0.25.
	ParagraphGapRatio float64

	// ParagraphIndentEm is the indent above the block median, in em, that
	// starts a new paragraph. Default 0.5.
	ParagraphIndentEm float64

	// ShortLineRatio is the fraction of block width below which a line
	// ending in terminal punctuation ends a paragraph. Default 0.8.
	ShortLineRatio float64

	// ScanMedianGlyphs is the median glyphs per page below which a document
	// is a candidate for the scanned classifier. Default 20.
	ScanMedianGlyphs int

	// ScanImagePageRatio is the fraction of sampled pages that must carry
	// page-covering images to confirm a scan. Default 0.8.
	ScanImagePageRatio float64

	// ScanSamplePages is how many pages the scanned classifier samples.
	// Default 20.
	ScanSamplePages int

	// ScanImageCoverRatio is the share of a page images must cover for it to
	// count as image-covered by the scanned classifier. Spec 6 describes
	// "full-page images"; 0.8 allows for the margins a scan usually keeps.
	ScanImageCoverRatio float64

	// MaxColumns caps automatic column detection. Default 3.
	MaxColumns int

	// GutterMinWidthSpaces is how many median space widths a whitespace band
	// must span to count as a column gutter. Default 2.
	GutterMinWidthSpaces float64

	// GutterMinHeightRatio is the fraction of text-carrying rows a band must
	// be empty across to count as a gutter. Default 0.6.
	GutterMinHeightRatio float64

	// ColumnMinGlyphRatio is the minimum share of a page's glyphs each
	// detected column must hold for the split to be believed. Default 0.1.
	//
	// Not in the spec. Section 4.4 notes the column heuristic misfires on
	// tables and figures; this guard rejects a split that would leave a
	// column nearly empty.
	ColumnMinGlyphRatio float64

	// ColumnMinRows is the number of text-carrying rows a page needs before
	// its projection profile is trusted at all. Default 8.
	//
	// Not in the spec, and the guard that matters most in practice. Asking
	// whether a band is empty across 60% of rows is meaningless on a page
	// with four rows, so title pages and figure pages otherwise produce
	// phantom gutters.
	ColumnMinRows int

	// ColumnMinLines is the number of assembled lines each detected column
	// must contain for the split to survive. Default 3.
	//
	// Not in the spec. Checked after lines are split at the gutters, which is
	// stronger evidence than the glyph share.
	ColumnMinLines int

	// HeadingSizeRatio is how far a block's font size must exceed the body
	// font to be a heading. Default 0.15, meaning 15% larger.
	HeadingSizeRatio float64

	// HeadingBoldMaxWords is the word count below which a bold block with no
	// terminal punctuation is a heading. Default 15.
	HeadingBoldMaxWords int

	// HeadingMaxWords caps the size-based heading test. Default 50.
	//
	// Not in the spec. Section 4.6 makes size alone sufficient, which would
	// turn a long epigraph or pull quote set slightly large into a heading
	// and split the book at it.
	HeadingMaxWords int

	// BackgroundCoverRatio is the share of the page an image must cover,
	// when painted beneath the text, to be dropped as a background or
	// watermark. Default 0.95.
	BackgroundCoverRatio float64

	// MinImagePoints is the smallest edge an image may have before it is
	// dropped. Default 16.
	MinImagePoints float64

	// MinImageAreaRatio is the share of page area below which an image is
	// dropped. Default 0.02.
	MinImageAreaRatio float64

	// InlineImageWidthRatio is the fraction of the text column width below
	// which an image inside a paragraph flows inline. Default 0.4.
	InlineImageWidthRatio float64

	// CaptionGapLines is how many line heights a caption may sit from its
	// figure. Default 1.5.
	CaptionGapLines float64

	// CaptionSizeRatio is how far below the body font a caption is set.
	// Default 0.05.
	CaptionSizeRatio float64

	// CaptionOverlapRatio is the horizontal overlap a block must share with
	// an image to be treated as its caption. Default 0.3.
	//
	// Not in the spec: without it a sidebar level with a figure binds to it.
	CaptionOverlapRatio float64

	// FurnitureBandRatio is the fraction of page height at the top and bottom
	// in which running heads and folios live. Default 0.08.
	FurnitureBandRatio float64

	// FurnitureRepeatRatio is the fraction of sampled pages a block must
	// repeat on to be removed as furniture. Default 0.6.
	FurnitureRepeatRatio float64

	// FurnitureSamplePages is how many pages the furniture sampler examines.
	// Default 20.
	FurnitureSamplePages int

	// FurnitureMinPages is the document length below which furniture removal
	// is skipped. Default 5.
	FurnitureMinPages int

	// QuoteIndentEm is how far both margins must be inset beyond the body, in
	// em, for a blockquote. Default 1.5.
	QuoteIndentEm float64

	// FootnoteBandRatio is the fraction of page height, from the bottom, in
	// which a footnote may sit. Default 0.2.
	FootnoteBandRatio float64

	// FootnoteSizeRatio is how far below the body font a footnote is set.
	// Default 0.1.
	FootnoteSizeRatio float64

	// SuperscriptRiseEm is the baseline offset, as a fraction of em, above
	// which a glyph counts as a superscript. Default 0.2.
	SuperscriptRiseEm float64

	// SuperscriptSizeRatio is the size ratio below which a raised glyph
	// counts as a superscript. Default 0.85.
	SuperscriptSizeRatio float64

	// VectorMinPaints is the number of painted paths a page must carry before
	// its vector artwork is reported as dropped content. Default 24.
	//
	// Not in the spec. Almost every PDF paints some paths for rules,
	// underlines, table borders, and form fields; reporting those as lost
	// artwork would be noise a reader cannot act on. A page actually drawing
	// a diagram paints far more, and the distributions do not overlap: across
	// the sample corpus incidental decoration runs from 7 to 20 paths per
	// page, while a mathematics textbook full of geometry figures paints
	// about forty. The default sits in that gap.
	//
	// The bias is deliberate. Reporting a form border as lost artwork is
	// noise; missing a dropped chart is the silent content loss this
	// diagnostic exists to end, so the threshold is set to catch artwork
	// rather than to catch every path.
	VectorMinPaints int
}

// DefaultHeuristics returns the documented defaults from spec section 4.
func DefaultHeuristics() Heuristics {
	return Heuristics{
		BaselineTolerance:    0.3,
		SpaceGapRatio:        0.25,
		RotationTolerance:    5,
		KeepRotated:          false,
		BlockOverlapRatio:    0.5,
		BlockGapRatio:        1.5,
		BlockSizeChangeRatio: 0.15,
		ParagraphGapRatio:    0.25,
		ParagraphIndentEm:    0.5,
		ShortLineRatio:       0.8,
		ScanMedianGlyphs:     20,
		ScanImagePageRatio:   0.8,
		ScanSamplePages:      20,
		ScanImageCoverRatio:  0.8,
		MaxColumns:           3,
		GutterMinWidthSpaces: 2,
		GutterMinHeightRatio: 0.6,
		ColumnMinGlyphRatio:  0.1,
		ColumnMinRows:        8,
		ColumnMinLines:       3,
		HeadingSizeRatio:     0.15,
		HeadingBoldMaxWords:  15,
		HeadingMaxWords:      50,

		BackgroundCoverRatio:  0.95,
		MinImagePoints:        16,
		MinImageAreaRatio:     0.02,
		InlineImageWidthRatio: 0.4,
		CaptionGapLines:       1.5,
		CaptionSizeRatio:      0.05,
		CaptionOverlapRatio:   0.3,

		FurnitureBandRatio:   0.08,
		FurnitureRepeatRatio: 0.6,
		FurnitureSamplePages: 20,
		FurnitureMinPages:    5,

		QuoteIndentEm:        1.5,
		FootnoteBandRatio:    0.2,
		FootnoteSizeRatio:    0.1,
		SuperscriptRiseEm:    0.2,
		SuperscriptSizeRatio: 0.85,
		VectorMinPaints:      24,
	}
}

// PageRange selects a subset of pages. The zero value selects every page.
type PageRange struct {
	// spans holds inclusive, zero-based, sorted, non-overlapping ranges.
	spans []span
}

type span struct{ lo, hi int }

// ParsePageRange parses the CLI form, e.g. "5-200,210". Page numbers are
// one-based in the text form and stored zero-based.
func ParsePageRange(s string) (PageRange, error) {
	var pr PageRange
	s = strings.TrimSpace(s)
	if s == "" {
		return pr, nil
	}

	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, err := parseSpan(part)
		if err != nil {
			return PageRange{}, err
		}
		pr.spans = append(pr.spans, span{lo, hi})
	}
	if len(pr.spans) == 0 {
		return PageRange{}, fmt.Errorf("page range %q selects no pages", s)
	}

	sort.Slice(pr.spans, func(i, j int) bool { return pr.spans[i].lo < pr.spans[j].lo })
	merged := pr.spans[:1]
	for _, sp := range pr.spans[1:] {
		last := &merged[len(merged)-1]
		if sp.lo <= last.hi+1 {
			if sp.hi > last.hi {
				last.hi = sp.hi
			}
			continue
		}
		merged = append(merged, sp)
	}
	pr.spans = merged
	return pr, nil
}

func parseSpan(part string) (int, int, error) {
	if i := strings.IndexByte(part, '-'); i > 0 {
		lo, err := strconv.Atoi(strings.TrimSpace(part[:i]))
		if err != nil {
			return 0, 0, fmt.Errorf("bad page range %q: %w", part, err)
		}
		hiText := strings.TrimSpace(part[i+1:])
		if hiText == "" {
			// Open-ended "12-" runs to the end of the document.
			if lo < 1 {
				return 0, 0, fmt.Errorf("bad page range %q: pages start at 1", part)
			}
			return lo - 1, int(^uint(0) >> 1), nil
		}
		hi, err := strconv.Atoi(hiText)
		if err != nil {
			return 0, 0, fmt.Errorf("bad page range %q: %w", part, err)
		}
		if lo < 1 || hi < lo {
			return 0, 0, fmt.Errorf("bad page range %q", part)
		}
		return lo - 1, hi - 1, nil
	}

	n, err := strconv.Atoi(part)
	if err != nil {
		return 0, 0, fmt.Errorf("bad page number %q: %w", part, err)
	}
	if n < 1 {
		return 0, 0, fmt.Errorf("bad page number %q: pages start at 1", part)
	}
	return n - 1, n - 1, nil
}

// All reports whether the range selects every page.
func (p PageRange) All() bool { return len(p.spans) == 0 }

// Contains reports whether the zero-based page index is selected.
func (p PageRange) Contains(i int) bool {
	if len(p.spans) == 0 {
		return true
	}
	for _, sp := range p.spans {
		if i >= sp.lo && i <= sp.hi {
			return true
		}
	}
	return false
}

// String renders the range in the CLI form.
func (p PageRange) String() string {
	if len(p.spans) == 0 {
		return "all"
	}
	parts := make([]string, 0, len(p.spans))
	maxInt := int(^uint(0) >> 1)
	for _, sp := range p.spans {
		switch {
		case sp.hi == maxInt:
			parts = append(parts, fmt.Sprintf("%d-", sp.lo+1))
		case sp.lo == sp.hi:
			parts = append(parts, strconv.Itoa(sp.lo+1))
		default:
			parts = append(parts, fmt.Sprintf("%d-%d", sp.lo+1, sp.hi+1))
		}
	}
	return strings.Join(parts, ",")
}

// ImageMode selects how images are handled.
type ImageMode string

const (
	// ImagesKeep retains images in their original color.
	ImagesKeep ImageMode = "keep"
	// ImagesGrayscale converts images to grayscale.
	ImagesGrayscale ImageMode = "grayscale"
	// ImagesDrop discards images.
	ImagesDrop ImageMode = "drop"
)

// Valid reports whether m is a known image mode.
func (m ImageMode) Valid() bool {
	switch m {
	case ImagesKeep, ImagesGrayscale, ImagesDrop:
		return true
	}
	return false
}

// Options configures a Converter. The zero value is not usable; start from
// DefaultOptions.
type Options struct {
	Profile    Profile
	Metadata   Metadata
	Pages      PageRange
	Heuristics Heuristics

	// SplitAt selects chapter boundaries. Default SplitAtH1.
	SplitAt SplitMode
	// MaxChunkBytes forces a split of oversized XHTML at a paragraph
	// boundary. Default 262144; the minimal profile lowers it to 65536.
	//
	// The crosspoint profile keeps 262144: its firmware streams XHTML rather
	// than parsing it into memory, so chapter size is not a memory constraint
	// there. See ApplyProfileDefaults.
	MaxChunkBytes int

	// KeepHeaders retains running heads and folios. Default false.
	KeepHeaders bool
	// NoDehyphenate preserves line-break hyphens verbatim.
	NoDehyphenate bool

	// Columns forces a column count. Zero detects from the page's projection
	// profile; 1, 2, or 3 override it. Spec 3: --columns.
	Columns int

	// Images selects image handling. Default ImagesKeep.
	Images ImageMode
	// KeepSmallImages retains images the size rules in spec 4.7 would drop.
	KeepSmallImages bool
	// ImageMaxWidth is the longest edge in pixels; 0 disables scaling.
	ImageMaxWidth int

	// Jobs is the page-parallel worker count. It must not affect output
	// bytes.
	Jobs int

	// Deterministic fixes the output timestamp. When zero, the PDF ModDate
	// is used, then the Unix epoch.
	Deterministic time.Time

	// Strict makes quality threshold breaches an error at the CLI layer.
	Strict bool
}

// DefaultOptions returns the documented defaults from spec section 3.
func DefaultOptions() Options {
	return Options{
		Profile:       ProfileStandard,
		Heuristics:    DefaultHeuristics(),
		SplitAt:       SplitAtH1,
		MaxChunkBytes: 262144,
		Images:        ImagesKeep,
		ImageMaxWidth: 1600,
		Jobs:          1,
	}
}

// ApplyProfileDefaults overwrites the image, chunk-size, and related fields
// with the device profile defaults from spec section 5.
//
// It overwrites unconditionally, so a caller that wants an explicit value to
// win must re-apply it afterward. The CLI does exactly that, using flag.Visit
// to learn which flags the user actually passed.
func (o *Options) ApplyProfileDefaults() {
	switch o.Profile {
	case ProfileCrossPoint:
		o.Images = ImagesGrayscale
		o.ImageMaxWidth = 480
		// Deliberately the same as the standard profile. Spec section 5.1
		// carried 65536 as a guess anchored to the ESP32-C3's ~380 KB of RAM,
		// on the assumption that a chapter is parsed into memory. Reading the
		// firmware settled it (spec section 13, closed 2026-08-01): XHTML is
		// streamed through expat in 1 KB chunks and each page is serialized to
		// the SD card and freed as it completes, so chapter size never lands
		// in RAM. The only term that scales with chapter length is a 12-byte
		// page lookup entry, about 0.9% of the chapter's bytes, which is noise
		// against the 60 KB of free heap the firmware requires just to decode
		// a PNG.
		o.MaxChunkBytes = 262144
	case ProfileMinimal:
		o.Images = ImagesDrop
		o.ImageMaxWidth = 0
		// Kept low here. Unlike crosspoint this profile names no device, so
		// there is no firmware to check; a reader that parses a whole chapter
		// into a DOM is the case it exists to serve.
		o.MaxChunkBytes = 65536
	}
}

// navDepth returns the TOC depth cap for the profile; 0 is unlimited.
func (o *Options) navDepth() int {
	switch o.Profile {
	case ProfileCrossPoint, ProfileMinimal:
		return 2
	}
	return 0
}

// validate checks the option set and returns a usage-level error.
func (o *Options) validate() error {
	if !o.Profile.Valid() {
		return fmt.Errorf("unknown profile %q (want standard, crosspoint, or minimal)", o.Profile)
	}
	if !o.SplitAt.Valid() {
		return fmt.Errorf("unknown split mode %q (want h1, h2, page, or none)", o.SplitAt)
	}
	if !o.Images.Valid() {
		return fmt.Errorf("unknown image mode %q (want keep, grayscale, or drop)", o.Images)
	}
	if o.MaxChunkBytes < 4096 {
		return fmt.Errorf("max chunk bytes %d is below the 4096 minimum", o.MaxChunkBytes)
	}
	if o.ImageMaxWidth < 0 {
		return fmt.Errorf("image max width %d is negative", o.ImageMaxWidth)
	}
	if o.Jobs < 0 {
		return fmt.Errorf("jobs %d is negative", o.Jobs)
	}
	if o.Columns < 0 || o.Columns > 3 {
		return fmt.Errorf("columns %d is out of range (want auto, 1, 2, or 3)", o.Columns)
	}
	return nil
}
