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
	return &Converter{opts: opts, cfg: layoutConfig(opts.Heuristics)}, nil
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

	pages := c.selectedPages(src.PageCount())
	if len(pages) == 0 {
		return nil, &UsageError{
			Err: fmt.Errorf("page range %s selects no pages of %d",
				c.opts.Pages.String(), src.PageCount()),
		}
	}
	rep.PagesConverted = len(pages)

	if c.opts.Jobs > 1 {
		rep.info("analyze", -1,
			"page-parallel processing is not enabled yet; --jobs is accepted but ignored")
	}

	// Stage 2 sample first, so a scanned document fails before any
	// segmentation work happens. Results are cached and reused below.
	cache := map[int]*pdf.PageContent{}
	if err := c.detectScanned(ctx, src, pages, cache, rep); err != nil {
		return nil, err
	}

	for _, idx := range pages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.analyzePage(src, idx, cache, doc, rep)
	}

	assignBlockIDs(doc.Blocks)
	for _, b := range doc.Blocks {
		rep.Blocks[b.Kind]++
	}
	doc.Outline = convertOutline(src.Outline())
	doc.Modified = c.resolveModTime(src.Info())

	if len(doc.Blocks) == 0 {
		rep.warn("classify", -1, "no content blocks were reconstructed")
	}
	return doc, nil
}

// analyzePage runs stages 2 through 6 for one page and appends its blocks.
func (c *Converter) analyzePage(src *pdf.Document, idx int, cache map[int]*pdf.PageContent, doc *Document, rep *Report) {
	m := PageMetrics{Page: idx}

	page, err := src.Page(idx)
	if err != nil {
		rep.warn("parse", idx, fmt.Sprintf("skipped: %v", err))
		rep.Pages = append(rep.Pages, m)
		return
	}

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

	pl := layout.AssembleLines(c.cfg, pc)
	m.Glyphs = pl.GlyphCount
	m.DecodeFailures = pl.DecodeFailures
	m.Lines = len(pl.Lines)
	m.UsedInvisibleText = pl.UsedInvisibleText

	if pl.UsedInvisibleText {
		rep.info("glyphs", idx,
			"page has no visible text; kept the invisible (Tr 3) layer, which is a searchable scan")
	}
	if !c.opts.Heuristics.KeepRotated && len(pl.Rotated) > 0 {
		m.RotatedDropped = len(pl.Rotated)
		rep.info("lines", idx,
			fmt.Sprintf("dropped %d rotated run(s) beyond %.0f degrees", len(pl.Rotated),
				c.opts.Heuristics.RotationTolerance))
	}
	if r := m.DecodeFailureRate(); r > 0.05 {
		rep.warn("glyphs", idx,
			fmt.Sprintf("%.1f%% of glyphs failed to decode to Unicode", r*100))
	}

	blocks := layout.SegmentBlocks(c.cfg, pl.Lines)
	m.Blocks = len(blocks)

	for _, b := range blocks {
		for _, p := range layout.Reconstruct(c.cfg, b) {
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			doc.Blocks = append(doc.Blocks, Block{
				// Structure classification is spec section 4.6 and lands in
				// M2; every block is a paragraph until then.
				Kind:   KindParagraph,
				Text:   p.Text,
				Page:   idx,
				Bounds: toRect(p.Bounds),
				Size:   p.Size,
				Font:   fontName(p.Font),
			})
		}
	}

	rep.Pages = append(rep.Pages, m)
}

// detectScanned implements the classifier in spec section 6: a document is a
// scan when its median glyph count per page is low and most pages carry
// page-covering images. Both conditions are required, which keeps it from
// misfiring on an image-heavy art book with sparse captions.
//
// Image coverage measurement needs the CTM at draw time, which arrives with
// image extraction in M3. Until then the image test is the weaker "page
// references an image XObject", so the glyph-count condition carries the
// decision.
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

		if src.HasImages(page) {
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

	chapters, nav := c.buildChapters(doc, rep)
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
			Children: convertOutline(it.Children),
		})
	}
	return out
}

func toRect(r pdf.Rect) Rect {
	return Rect{MinX: r.MinX, MinY: r.MinY, MaxX: r.MaxX, MaxY: r.MaxY}
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
