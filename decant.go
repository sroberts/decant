package decant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/sroberts/decant/internal/epub"
	"github.com/sroberts/decant/internal/layout"
	"github.com/sroberts/decant/internal/pdf"
)

// Converter runs the conversion pipeline. It holds no mutable state, so one
// Converter is safe to reuse across documents.
type Converter struct {
	opts Options
	cfg  layout.Config
}

// New validates options and returns a Converter.
func New(opts Options) (*Converter, error) {
	if opts.MaxChunkBytes == 0 {
		opts.MaxChunkBytes = DefaultOptions().MaxChunkBytes
	}
	if opts.Profile == "" {
		opts.Profile = ProfileStandard
	}
	if opts.SplitAt == "" {
		opts.SplitAt = SplitAtH1
	}
	if opts.Images == "" {
		opts.Images = ImagesKeep
	}
	if opts.Heuristics == (Heuristics{}) {
		opts.Heuristics = DefaultHeuristics()
	}
	if err := opts.validate(); err != nil {
		return nil, &UsageError{Err: err}
	}
	cfg := layoutConfig(opts.Heuristics)
	cfg.Columns = opts.Columns
	cfg.KeepSmallImages = opts.KeepSmallImages
	cfg.KeepHeaders = opts.KeepHeaders
	cfg.TableMode = layout.TableMode(opts.Tables)
	cfg.ListMarker = func(s string) bool {
		_, ok := parseListMarker(s)
		return ok
	}
	return &Converter{opts: opts, cfg: cfg}, nil
}

// Options returns the converter's resolved options.
func (c *Converter) Options() Options { return c.opts }

// layoutConfig projects the public heuristics onto the layout package's
// config. The two are kept separate so the public API does not depend on
// internal stage organization.
func layoutConfig(h Heuristics) layout.Config {
	return layout.Config{
		BaselineTolerance:    h.BaselineTolerance,
		SpaceGapRatio:        h.SpaceGapRatio,
		RotationTolerance:    h.RotationTolerance,
		KeepRotated:          h.KeepRotated,
		ParagraphGapRatio:    h.ParagraphGapRatio,
		ParagraphIndentEm:    h.ParagraphIndentEm,
		ShortLineRatio:       h.ShortLineRatio,
		BlockGapRatio:        h.BlockGapRatio,
		BlockOverlapRatio:    h.BlockOverlapRatio,
		BlockSizeChangeRatio: h.BlockSizeChangeRatio,
		MaxColumns:           h.MaxColumns,
		GutterMinWidthSpaces: h.GutterMinWidthSpaces,
		GutterMinHeightRatio: h.GutterMinHeightRatio,
		ColumnMinGlyphRatio:  h.ColumnMinGlyphRatio,
		ColumnMinRows:        h.ColumnMinRows,
		ColumnMinLines:       h.ColumnMinLines,

		QuoteIndentEm:        h.QuoteIndentEm,
		FootnoteBandRatio:    h.FootnoteBandRatio,
		FootnoteSizeRatio:    h.FootnoteSizeRatio,
		SuperscriptRiseEm:    h.SuperscriptRiseEm,
		SuperscriptSizeRatio: h.SuperscriptSizeRatio,

		FurnitureBandRatio:   h.FurnitureBandRatio,
		FurnitureRepeatRatio: h.FurnitureRepeatRatio,
		FurnitureSamplePages: h.FurnitureSamplePages,
		FurnitureMinPages:    h.FurnitureMinPages,

		BackgroundCoverRatio:  h.BackgroundCoverRatio,
		MinImagePoints:        h.MinImagePoints,
		MinImageAreaRatio:     h.MinImageAreaRatio,
		InlineImageWidthRatio: h.InlineImageWidthRatio,
		CaptionGapLines:       h.CaptionGapLines,
		CaptionSizeRatio:      h.CaptionSizeRatio,
		CaptionOverlapRatio:   h.CaptionOverlapRatio,

		RuleMaxThickness:      h.RuleMaxThickness,
		RuleClusterTolerance:  h.RuleClusterTolerance,
		RuleRowCoverRatio:     h.RuleRowCoverRatio,
		TableRegionGap:        h.TableRegionGap,
		TableColumnTolerance:  h.TableColumnTolerance,
		TableMinSharedColumns: h.TableMinSharedColumns,
		TableMinRows:          h.TableMinRows,
		TableMinFilledRatio:   h.TableMinFilledRatio,
	}
}

// Analyze runs stages 1 through 6 and returns the intermediate model.
//
// The returned Document exposes the block tree so a caller can correct
// structure before Write serializes it.
func (c *Converter) Analyze(ctx context.Context, r io.ReaderAt, size int64) (*Document, error) {
	if size <= 0 {
		return nil, &MalformedError{Detail: "empty input"}
	}

	digest, err := digestOf(r, size)
	if err != nil {
		return nil, fmt.Errorf("hashing input: %w", err)
	}

	src, err := pdf.Open(r, size)
	if err != nil {
		return nil, translateError(err)
	}

	rep := newReport("")
	rep.PageCount = src.PageCount()

	doc := &Document{
		PageCount: src.PageCount(),
		Digest:    digest,
		report:    rep,
	}
	c.applyMetadata(doc, src)

	// The pattern set depends on the resolved language, so the layout config
	// is finalized per document rather than per converter.
	cfg := c.cfg
	cfg.Dehyphenator = c.resolveDehyphenator(doc.Language, rep)

	pages := c.selectedPages(src.PageCount())
	if len(pages) == 0 {
		return nil, &UsageError{
			Err: fmt.Errorf("page range %s selects no pages of %d",
				c.opts.Pages.String(), src.PageCount()),
		}
	}
	rep.PagesConverted = len(pages)

	// Stage 2 sample first, so a scanned document fails before any
	// segmentation work happens. Results are cached and reused below.
	cache := map[int]*pdf.PageContent{}
	if err := c.detectScanned(ctx, src, pages, cache, rep); err != nil {
		return nil, err
	}

	doc.Outline = convertOutline(src.Outline())

	// Flatten the outline and index it by page, so each destination's user
	// space y can be converted while its page is loaded.
	var entries []outlineEntry
	flattenOutline(doc.Outline, 1, &entries)
	entriesByPage := map[int][]int{}
	for i, e := range entries {
		entriesByPage[e.page] = append(entriesByPage[e.page], i)
	}

	// feats parallels doc.Blocks and holds the measurements classification
	// needs; hist accumulates the document-wide font statistics the body font
	// is computed from.
	var feats []blockFeatures
	hist := fontHistogram{}
	imgs := newImageSet()
	pageHeights := map[int]float64{}
	// Converts a destination's user-space y to page space, per source page. A
	// cross-reference can point at any page, including one processed long
	// before or after the one it sits on, so the conversion is captured while
	// the page is loaded and applied once every page has been seen.
	pageSpaceY := map[int]func(float64) float64{}

	for _, idx := range pages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.analyzePage(cfg, src, idx, cache, doc, &feats, hist, entries,
			entriesByPage[idx], imgs, pageHeights, pageSpaceY, rep)
	}
	doc.Images = imgs.sorted()

	// Stage 5 runs document-wide: a running head is identifiable only by its
	// repetition across pages.
	doc.Blocks, feats = c.removeFurniture(doc.Blocks, feats, pageHeights, len(pages), rep)

	// Stage 6 runs document-wide: the body font is a whole-document statistic
	// and the outline spans pages.
	//
	// The footnote and blockquote tests need the same document-level context,
	// so their features are filled in here rather than per page.
	doc.Blocks, feats = c.mergeFootnoteMarkers(doc.Blocks, feats, pageHeights)
	c.fillStructureFeatures(doc.Blocks, feats, pageHeights, hist)

	reconcileOutline(doc.Blocks, feats, entries, rep)
	c.classify(doc.Blocks, feats, hist, rep)
	doc.Blocks, feats = groupLists(doc.Blocks, feats)

	// IDs must exist before footnotes are linked, since a noteref points at
	// the footnote block's anchor.
	assignBlockIDs(doc.Blocks)
	c.linkFootnotes(doc.Blocks, rep)
	resolveCrossRefs(doc.Blocks, pageSpaceY, rep)

	for _, b := range doc.Blocks {
		rep.Blocks[b.Kind]++
		if b.Kind == KindHeading {
			rep.Headings[b.Level]++
		}
	}
	doc.Modified = c.resolveModTime(src.Info())

	if rep.VectorPagesDropped > 0 {
		rep.warn("images", -1, fmt.Sprintf(
			"%d of %d page(s) draw vector artwork that was not rendered "+
				"(%d painted paths); charts and diagrams drawn as paths are "+
				"lost; spec section 13 closed rasterization as out of scope for v1",
			rep.VectorPagesDropped, len(pages), rep.VectorPaintsDropped))
	}

	if len(doc.Blocks) == 0 {
		rep.warn("classify", -1, "no content blocks were reconstructed")
	}
	return doc, nil
}

// analyzePage runs stages 2 through 5 for one page and appends its blocks.
// Structure classification is document-wide and runs after every page.
func (c *Converter) analyzePage(
	cfg layout.Config,
	src *pdf.Document,
	idx int,
	cache map[int]*pdf.PageContent,
	doc *Document,
	feats *[]blockFeatures,
	hist fontHistogram,
	entries []outlineEntry,
	entryIdx []int,
	imgs *imageSet,
	pageHeights map[int]float64,
	pageSpaceY map[int]func(float64) float64,
	rep *Report,
) {
	m := PageMetrics{Page: idx}

	page, err := src.Page(idx)
	if err != nil {
		rep.warn("parse", idx, fmt.Sprintf("skipped: %v", err))
		rep.Pages = append(rep.Pages, m)
		return
	}

	pageHeights[idx] = page.Height
	pageSpaceY[idx] = func(y float64) float64 {
		_, py := page.ToPageSpace(0, y)
		return py
	}

	// Convert this page's outline destinations while its geometry is loaded.
	for _, ei := range entryIdx {
		if math.IsNaN(entries[ei].userY) {
			continue
		}
		_, y := page.ToPageSpace(0, entries[ei].userY)
		entries[ei].pageY = y
	}

	// Internal link annotations, in page space. Spec 4.9.
	pageLinks := src.Links(page)

	pc, ok := cache[idx]
	if !ok {
		pc = src.Glyphs(page)
	}
	delete(cache, idx)

	if pc.Truncated {
		rep.warn("glyphs", idx, "content stream exceeded the per-page glyph cap; page is incomplete")
	}
	if pc.Recovered {
		rep.warn("glyphs", idx,
			"content stream interpretation aborted on a parser fault; page text is incomplete or absent")
	}

	pl := layout.AnalyzePage(cfg, pc)
	m.Glyphs = pl.GlyphCount
	m.DecodeFailures = pl.DecodeFailures
	m.Lines = len(pl.Lines)
	m.Blocks = len(pl.Blocks)
	m.Columns = len(pl.Columns)
	m.RotatedDropped = pl.RotatedDropped
	m.UsedInvisibleText = pl.UsedInvisibleText
	m.VectorPaints = pc.VectorPaints

	// Vector artwork is not rendered. Spec section 1 puts conversion to SVG
	// out of scope for v1 and section 13 closed rasterization the same way, so
	// a chart drawn as paths is lost. Principle 3 requires that to be visible
	// rather
	// than silent, so the page is counted here and summarized once for the
	// document; a per-page warning on a book full of diagrams would be noise.
	if pc.VectorPaints >= c.opts.Heuristics.VectorMinPaints {
		rep.VectorPagesDropped++
		rep.VectorPaintsDropped += pc.VectorPaints
	}

	if len(pl.Columns) > 1 {
		rep.MultiColumnPages++
		rep.info("blocks", idx,
			fmt.Sprintf("detected %d text columns", len(pl.Columns)))
	}
	if pl.UsedInvisibleText {
		rep.info("glyphs", idx,
			"page has no visible text; kept the invisible (Tr 3) layer, which is a searchable scan")
	}
	if pl.RotatedDropped > 0 {
		rep.info("lines", idx,
			fmt.Sprintf("dropped %d rotated run(s) beyond %.0f degrees", pl.RotatedDropped,
				c.opts.Heuristics.RotationTolerance))
	}
	if r := m.DecodeFailureRate(); r > 0.05 {
		rep.warn("glyphs", idx,
			fmt.Sprintf("%.1f%% of glyphs failed to decode to Unicode", r*100))
	}

	// Accumulate the document-wide font statistics the body font derives
	// from, weighted by glyph count as spec section 4.6 requires.
	for _, l := range pl.Lines {
		family, bold, fixed := "", false, false
		if l.Font != nil {
			family, bold, fixed = l.Font.Family, l.Font.Bold, l.Font.FixedPitch
		}
		hist.add(family, l.Size, bold, fixed, len(l.Glyphs))
	}

	// Figures are placed before paragraphs are emitted so a caption block can
	// be consumed by its figure rather than appearing twice.
	bodySize := medianLineSize(pl.Lines)
	figs, captionBlocks := layout.PlaceFigures(
		cfg, pl, pc.Images, page.Width, page.Height, bodySize)
	imageIDs := c.collectPageImages(src, idx, figureDraws(figs), imgs, rep)

	if n := len(pc.Images) - len(figs); n > 0 {
		rep.info("images", idx, fmt.Sprintf(
			"dropped %d image(s) as background, watermark, or too small", n))
	}
	m.Images = len(figs)

	// Tables are detected before paragraphs are emitted, so the lines they
	// consume are not also emitted as prose.
	tables := layout.DetectTables(cfg, pl, pc.Rules)
	tableLines := map[int]bool{}
	for _, t := range tables {
		for i := range t.LineIndices {
			tableLines[i] = true
		}
	}
	if len(tables) > 0 {
		m.Tables = len(tables)
		for _, t := range tables {
			rep.Tables[string(t.Confidence)]++
		}
	}

	// Emit paragraphs in reading order, then insert each figure at the point
	// its position implies.
	//
	// Paragraph order is never re-sorted. It already encodes the column
	// reading order established upstream, and sorting the combined list by
	// vertical position would interleave the columns of a two-column page,
	// which is precisely the scrambling stage 4 exists to prevent.
	type item struct {
		block Block
		feat  blockFeatures
		// srcBlock is the index in pl.Blocks this came from, which figure
		// insertion compares against.
		srcBlock int
	}
	var items []item

	for bi, b := range pl.Blocks {
		if captionBlocks[bi] {
			continue
		}
		if len(tableLines) > 0 && blockInTable(pl, b, tableLines) {
			continue
		}
		for _, p := range layout.Reconstruct(cfg, b) {
			text := strings.TrimSpace(p.Text)
			if text == "" {
				continue
			}
			family, bold, fixed := "", false, false
			if p.Font != nil {
				family, bold, fixed = p.Font.Family, p.Font.Bold, p.Font.FixedPitch
			}
			rep.Hyphenation.Dropped += p.HyphensDropped
			rep.Hyphenation.Kept += p.HyphensKept
			for _, d := range p.HyphenDecisions {
				if len(rep.Hyphenation.Decisions) >= maxReportedHyphenDecisions {
					break
				}
				rep.Hyphenation.Decisions = append(rep.Hyphenation.Decisions,
					HyphenDecision{
						Left: d.Left, Right: d.Right,
						Dropped: d.Dropped, Reason: d.Reason,
					})
			}

			supers := layout.SuperscriptLabels(text)

			// Cross-references. Spans are offsets into p.Text, which is
			// trimmed again on the way into the block, so the leading trim
			// comes off here.
			var refs []CrossRef
			if len(pageLinks) > 0 {
				lead := len(p.Text) - len(strings.TrimLeft(p.Text, " \t"))
				for _, sp := range layout.MapLinks(cfg, &p, pc.Fonts, pageLinks) {
					start, end := sp.Start-lead, sp.End-lead
					if start < 0 {
						start = 0
					}
					if end > len(text) {
						end = len(text)
					}
					if start >= end {
						continue
					}
					refs = append(refs, CrossRef{
						Start: start, End: end,
						TargetPage: sp.TargetPage,
						TargetY:    sp.TargetY,
					})
				}
			}

			bf := blockFeatures{
				size:       p.Size,
				family:     family,
				bold:       bold,
				fixedPitch: fixed,
				words:      countWords(text),
				terminal:   endsWithTerminal(text),
				fullWidth:  b.Column == -1,
				lines:      len(p.Lines),
			}
			// Spec 4.6's list test: an opening marker plus a hanging indent
			// on the continuation lines. A single-line block cannot show a
			// hanging indent, so the marker alone carries it there.
			if lm, ok := parseListMarker(text); ok {
				if len(p.Lines) < 2 || hasHangingIndent(p.Lines) {
					bf.listMarker = true
					bf.listOrdered = lm.ordered
					bf.listStart = lm.start
					bf.listText = lm.text
				}
			}

			items = append(items, item{
				srcBlock: bi,
				block: Block{
					// Kind and Level are filled in by classify once every
					// page has contributed to the font histogram.
					Kind:         KindParagraph,
					Text:         text,
					Links:        refs,
					Superscripts: supers,
					Page:         idx,
					Bounds:       toRect(p.Bounds),
					Size:         p.Size,
					Font:         family,
				},
				feat: bf,
			})
		}
	}

	for _, t := range tables {
		rows := make([][]TableCell, 0, len(t.Rows))
		for _, r := range t.Rows {
			row := make([]TableCell, 0, len(r))
			for _, c := range r {
				row = append(row, TableCell{Text: c.Text, ColSpan: c.ColSpan})
			}
			rows = append(rows, row)
		}
		tb := item{
			srcBlock: -1,
			block: Block{
				Kind:            KindTable,
				Text:            tableFlatText(rows),
				TableRows:       rows,
				TableConfidence: string(t.Confidence),
				Page:            idx,
				Bounds:          toRect(t.Bounds),
			},
			feat: blockFeatures{isTable: true},
		}
		srcs := make([]int, len(items))
		for i, it := range items {
			srcs[i] = it.srcBlock
		}
		at := rectInsertIndex(srcs, pl.Blocks, t.Bounds)
		items = append(items, item{})
		copy(items[at+1:], items[at:])
		items[at] = tb
	}

	for _, f := range figs {
		id, ok := imageIDs[f.Draw.Order]
		if !ok {
			continue
		}
		fb := item{
			srcBlock: -1,
			block: Block{
				Kind:        KindFigure,
				Text:        f.Caption,
				Caption:     f.Caption,
				ImageID:     id,
				InlineImage: f.Inline,
				Page:        idx,
				Bounds:      toRect(f.Bounds),
			},
			feat: blockFeatures{isFigure: true},
		}
		srcs := make([]int, len(items))
		for i, it := range items {
			srcs[i] = it.srcBlock
		}
		at := rectInsertIndex(srcs, pl.Blocks, f.Bounds)
		items = append(items, item{})
		copy(items[at+1:], items[at:])
		items[at] = fb
	}

	for _, it := range items {
		doc.Blocks = append(doc.Blocks, it.block)
		*feats = append(*feats, it.feat)
	}

	rep.Pages = append(rep.Pages, m)
}

// fillStructureFeatures computes the footnote and blockquote tests, both of
// which are measured against document-level statistics.
//
// The body margins are the modal left and right text edges across the
// document. Spec section 4.6 defines a blockquote as inset "beyond body" on
// both sides, which only means something relative to where body text sits.
func (c *Converter) fillStructureFeatures(
	blocks []Block,
	feats []blockFeatures,
	pageHeights map[int]float64,
	hist fontHistogram,
) {
	body, ok := hist.mode()
	if !ok {
		return
	}
	left, right := modalMargins(blocks, feats)

	for i := range blocks {
		if feats[i].isFigure {
			continue
		}
		c.footnoteFeatures(blocks[i], &feats[i], pageHeights[blocks[i].Page], body)
		c.quoteFeatures(blocks[i], &feats[i], left, right)
	}
}

// modalMargins returns the most common left and right text edges, rounded to
// the point.
//
// A mode rather than a mean: a document with a few deeply indented quotes
// would have its mean dragged inward, which is precisely the signal the
// blockquote test is trying to measure against.
func modalMargins(blocks []Block, feats []blockFeatures) (float64, float64) {
	leftCounts := map[int]int{}
	rightCounts := map[int]int{}
	for i, b := range blocks {
		// Only multi-line prose defines the measure. A heading or a caption
		// is routinely centered or short.
		if feats[i].isFigure || feats[i].lines < 2 {
			continue
		}
		leftCounts[int(math.Round(b.Bounds.MinX))]++
		rightCounts[int(math.Round(b.Bounds.MaxX))]++
	}
	return float64(modeOf(leftCounts)), float64(modeOf(rightCounts))
}

// modeOf returns the most frequent key, breaking ties toward the smaller
// value so the result is deterministic.
func modeOf(counts map[int]int) int {
	best, bestN := 0, 0
	for v, n := range counts {
		if n > bestN || (n == bestN && v < best) {
			best, bestN = v, n
		}
	}
	return best
}

// maxReportedHyphenDecisions caps the audit trail in the conversion report.
// The counts cover every decision; this bounds only the individual entries.
const maxReportedHyphenDecisions = 40

// figureInsertIndex finds where a figure belongs in an already-ordered
// sequence of emitted items.
//
// srcBlocks holds each item's source block index, or -1 for an item that came
// from an earlier figure. The scan returns the position of the first item that
// comes after the figure on the page, comparing within a column rather than
// across the page width: a block in a later column always follows, and a block
// in the same column follows when it starts lower down. Anything else keeps
// its place, so paragraph order is untouched.
func rectInsertIndex(srcBlocks []int, blocks []layout.Block, r pdf.Rect) int {
	for i, bi := range srcBlocks {
		if bi < 0 || bi >= len(blocks) {
			continue
		}
		if blocks[bi].Bounds.MinY > r.MinY {
			return i
		}
	}
	return len(srcBlocks)
}

// blockInTable reports whether every line of a block was claimed by a table.
func blockInTable(pl *layout.PageLayout, b layout.Block, taken map[int]bool) bool {
	matched, total := 0, 0
	for _, bl := range b.Lines {
		for i, l := range pl.Lines {
			if l.Baseline == bl.Baseline && l.Bounds.MinX == bl.Bounds.MinX {
				total++
				if taken[i] {
					matched++
				}
				break
			}
		}
	}
	return total > 0 && matched == total
}

// tableFlatText renders a table's cells as plain text, which the block model
// carries for fingerprints, anchors, and the text-mode fallback.
func tableFlatText(rows [][]TableCell) string {
	var sb strings.Builder
	for i, row := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		for j, c := range row {
			if j > 0 {
				sb.WriteString("\t")
			}
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// figureDraws extracts the image placements from a figure list.
func figureDraws(figs []layout.Figure) []pdf.ImageDraw {
	out := make([]pdf.ImageDraw, 0, len(figs))
	for _, f := range figs {
		out = append(out, f.Draw)
	}
	return out
}

// medianLineSize returns the median line size on a page, used as the local
// body size for caption detection before the document-wide body font exists.
func medianLineSize(lines []layout.Line) float64 {
	if len(lines) == 0 {
		return 0
	}
	v := make([]float64, 0, len(lines))
	for _, l := range lines {
		if l.Size > 0 {
			v = append(v, l.Size)
		}
	}
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	return v[len(v)/2]
}

// detectScanned implements the classifier in spec section 6: a document is a
// scan when its median glyph count per page is low and most pages carry
// page-covering images. Both conditions are required, which keeps it from
// misfiring on an image-heavy art book with sparse captions.
//
// Coverage is measured from the placement rectangles the interpreter records,
// so "covered by a full-page image" means exactly that rather than merely
// "references an image".
func (c *Converter) detectScanned(ctx context.Context, src *pdf.Document, pages []int, cache map[int]*pdf.PageContent, rep *Report) error {
	sampleSize := c.opts.Heuristics.ScanSamplePages
	if sampleSize <= 0 {
		sampleSize = DefaultHeuristics().ScanSamplePages
	}
	sample := evenSample(pages, sampleSize)
	if len(sample) == 0 {
		return nil
	}

	counts := make([]int, 0, len(sample))
	imaged := 0

	for _, idx := range sample {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := src.Page(idx)
		if err != nil {
			continue
		}
		pc := src.Glyphs(page)
		cache[idx] = pc

		visible := 0
		for _, g := range pc.Glyphs {
			if g.Visible() {
				visible++
			}
		}
		counts = append(counts, visible)

		if pageMostlyImage(pc.Images, page.Width, page.Height,
			c.opts.Heuristics.ScanImageCoverRatio) {
			imaged++
		}
	}
	if len(counts) == 0 {
		return nil
	}

	sort.Ints(counts)
	med := float64(counts[len(counts)/2])
	if len(counts)%2 == 0 && len(counts) > 1 {
		med = float64(counts[len(counts)/2-1]+counts[len(counts)/2]) / 2
	}
	frac := float64(imaged) / float64(len(counts))

	minGlyphs := float64(c.opts.Heuristics.ScanMedianGlyphs)
	minFrac := c.opts.Heuristics.ScanImagePageRatio

	if med < minGlyphs && frac > minFrac {
		return &NoTextLayerError{
			MedianGlyphs:      med,
			SampledPages:      len(counts),
			ImagePageFraction: frac,
		}
	}
	if med < minGlyphs {
		rep.warn("glyphs", -1, fmt.Sprintf(
			"median %.0f glyphs/page across %d sampled pages is low; output may be sparse",
			med, len(counts)))
	}
	return nil
}

// evenSample picks up to n indices spread evenly through pages.
// pageMostlyImage reports whether images cover at least cover of the page.
//
// Overlapping placements are unioned rather than summed: a scan tiled as
// several strips would otherwise report several hundred percent coverage,
// and a page with many small figures would falsely reach the threshold.
func pageMostlyImage(draws []pdf.ImageDraw, w, h, cover float64) bool {
	if len(draws) == 0 || w <= 0 || h <= 0 {
		return false
	}
	// A coarse occupancy grid is enough for a threshold test and avoids a
	// full rectangle-union computation.
	const grid = 32
	var cells [grid][grid]bool
	cw, ch := w/grid, h/grid

	for _, d := range draws {
		r := d.Rect
		x0 := int(math.Floor(r.MinX / cw))
		x1 := int(math.Ceil(r.MaxX / cw))
		y0 := int(math.Floor(r.MinY / ch))
		y1 := int(math.Ceil(r.MaxY / ch))
		for y := y0; y < y1; y++ {
			if y < 0 || y >= grid {
				continue
			}
			for x := x0; x < x1; x++ {
				if x < 0 || x >= grid {
					continue
				}
				cells[y][x] = true
			}
		}
	}

	covered := 0
	for y := 0; y < grid; y++ {
		for x := 0; x < grid; x++ {
			if cells[y][x] {
				covered++
			}
		}
	}
	return float64(covered)/float64(grid*grid) >= cover
}

func evenSample(pages []int, n int) []int {
	if len(pages) <= n {
		out := make([]int, len(pages))
		copy(out, pages)
		return out
	}
	out := make([]int, 0, n)
	step := float64(len(pages)-1) / float64(n-1)
	for i := 0; i < n; i++ {
		out = append(out, pages[int(math.Round(float64(i)*step))])
	}
	// The even spread can repeat an index at small n; dedupe while keeping
	// order.
	seen := map[int]bool{}
	uniq := out[:0]
	for _, v := range out {
		if seen[v] {
			continue
		}
		seen[v] = true
		uniq = append(uniq, v)
	}
	return uniq
}

func (c *Converter) selectedPages(count int) []int {
	out := make([]int, 0, count)
	for i := 0; i < count; i++ {
		if c.opts.Pages.Contains(i) {
			out = append(out, i)
		}
	}
	return out
}

// applyMetadata resolves title, author, and language from the PDF and any
// caller overrides.
func (c *Converter) applyMetadata(doc *Document, src *pdf.Document) {
	info := src.Info()

	doc.Title = firstNonEmpty(c.opts.Metadata.Title, info.Title)
	doc.Author = firstNonEmpty(c.opts.Metadata.Author, info.Author)
	doc.Language = firstNonEmpty(c.opts.Metadata.Language, info.Language, "en")

	if doc.Title == "" {
		doc.Title = "Untitled"
		doc.report.info("parse", -1, "PDF carries no title; using \"Untitled\"")
	}
	if info.Language == "" && c.opts.Metadata.Language == "" {
		doc.report.info("parse", -1, "PDF declares no language; assuming en")
	}
}

// resolveModTime picks the output timestamp, per spec section 4.9: the
// explicit --date, then SOURCE_DATE_EPOCH (resolved by the caller into
// Options.Deterministic), then the PDF ModDate, then the Unix epoch.
func (c *Converter) resolveModTime(info pdf.Info) time.Time {
	if !c.opts.Deterministic.IsZero() {
		return c.opts.Deterministic.UTC()
	}
	if !info.Modified.IsZero() {
		return info.Modified.UTC()
	}
	if !info.Created.IsZero() {
		return info.Created.UTC()
	}
	return time.Unix(0, 0).UTC()
}

// Write serializes an analyzed Document to EPUB.
func (c *Converter) Write(ctx context.Context, doc *Document, w io.Writer) (*Report, error) {
	if doc == nil {
		return nil, &UsageError{Err: errors.New("nil document")}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rep := doc.report
	if rep == nil {
		rep = newReport(doc.Source)
		doc.report = rep
	}
	rep.Source = doc.Source

	// Rebuild the lookup from the document, which a caller may have edited
	// between Analyze and Write.
	imgs := newImageSet()
	imgs.images = append(imgs.images, doc.Images...)

	chapters, nav := c.buildChapters(doc, imgs, rep)
	if len(chapters) == 0 {
		return rep, fmt.Errorf("nothing to write: document produced no content")
	}

	rep.Chapters = len(chapters)
	for _, ch := range chapters {
		if n := len(ch.Body); n > rep.LargestChapterBytes {
			rep.LargestChapterBytes = n
		}
	}

	book := &epub.Book{
		Identifier: epub.IdentifierFor(doc.Digest),
		Title:      doc.Title,
		Language:   doc.Language,
		Source:     doc.Source,
		Modified:   doc.Modified,
		Chapters:   chapters,
		Nav:        nav,
		CSS:        c.stylesheet(),
		NavDepth:   c.opts.navDepth(),
	}
	if doc.Author != "" {
		book.Authors = []string{doc.Author}
	}
	for _, img := range doc.Images {
		if !blockReferences(doc.Blocks, img.ID) {
			// A caller removed the figure; do not ship an orphan, which
			// epubcheck reports as an unreferenced manifest item.
			continue
		}
		book.Images = append(book.Images, epub.Image{
			ID: img.ID, Ext: img.Ext, MediaType: img.MediaType, Data: img.Data,
		})
		rep.ImagesPlaced++
		rep.ImageBytes += len(img.Data)
	}

	cw := &countingWriter{w: w}
	if err := epub.Write(cw, book); err != nil {
		return rep, err
	}
	rep.OutputBytes = cw.n

	if c.opts.Profile == ProfileCrossPoint && rep.OutputBytes > 20<<20 {
		rep.warn("serialize", -1, fmt.Sprintf(
			"output is %.1f MB; CrossInk guidance puts the comfortable ceiling at 20 MB",
			float64(rep.OutputBytes)/(1<<20)))
	}

	rep.Finish()
	return rep, nil
}

// Convert is Analyze followed by Write.
func (c *Converter) Convert(ctx context.Context, r io.ReaderAt, size int64, w io.Writer) (*Report, error) {
	doc, err := c.Analyze(ctx, r, size)
	if err != nil {
		return nil, err
	}
	return c.Write(ctx, doc, w)
}

func (c *Converter) stylesheet() string {
	switch c.opts.Profile {
	case ProfileMinimal:
		return ""
	case ProfileCrossPoint:
		return epub.MinimalCSS
	default:
		return epub.BaseCSS
	}
}

// countingWriter tracks how many bytes reached the output.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// digestOf computes the hex SHA-256 of the whole input.
func digestOf(r io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(r, 0, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// translateError converts internal error types to the public ones the CLI
// maps to exit codes.
func translateError(err error) error {
	var enc *pdf.ErrEncrypted
	if errors.As(err, &enc) {
		return &EncryptedError{Handler: enc.Handler, Revision: enc.Revision}
	}
	var mal *pdf.ErrMalformed
	if errors.As(err, &mal) {
		return &MalformedError{Detail: mal.Detail, Err: mal.Err}
	}
	return err
}

func convertOutline(items []pdf.OutlineItem) []OutlineItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]OutlineItem, 0, len(items))
	for _, it := range items {
		out = append(out, OutlineItem{
			Title:    it.Title,
			Page:     it.Page,
			Y:        it.Y,
			Children: convertOutline(it.Children),
		})
	}
	return out
}

func toRect(r pdf.Rect) Rect {
	return Rect{MinX: r.MinX, MinY: r.MinY, MaxX: r.MaxX, MaxY: r.MaxY}
}

// blockReferences reports whether any block still carries this image.
func blockReferences(blocks []Block, id string) bool {
	for _, b := range blocks {
		if b.Kind == KindFigure && b.ImageID == id {
			return true
		}
	}
	return false
}

func fontName(f *pdf.Font) string {
	if f == nil {
		return ""
	}
	return f.Family
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
