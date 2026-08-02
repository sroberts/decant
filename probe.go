package decant

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sroberts/decant/internal/epub"
	"github.com/sroberts/decant/internal/layout"
	"github.com/sroberts/decant/internal/pdf"
)

// DocumentInfo is the metadata the meta subcommand reports. Reading it does
// not run the conversion pipeline.
type DocumentInfo struct {
	Source    string `json:"source"`
	PageCount int    `json:"page_count"`

	Title    string `json:"title,omitempty"`
	Author   string `json:"author,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Keywords string `json:"keywords,omitempty"`
	Creator  string `json:"creator,omitempty"`
	Producer string `json:"producer,omitempty"`
	Language string `json:"language,omitempty"`

	Created  time.Time `json:"created,omitempty"`
	Modified time.Time `json:"modified,omitempty"`

	// Digest is the hex SHA-256 of the input file.
	Digest string `json:"digest"`
	// Identifier is the EPUB dc:identifier a conversion would produce.
	Identifier string `json:"identifier"`

	// OutlineEntries is the number of top-level bookmarks.
	OutlineEntries int           `json:"outline_entries"`
	Outline        []OutlineItem `json:"outline,omitempty"`

	// PageSizes lists the distinct page dimensions in points.
	PageSizes []PageSize `json:"page_sizes,omitempty"`
}

// PageSize is one distinct page geometry and how many pages share it.
type PageSize struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Rotate int     `json:"rotate"`
	Pages  int     `json:"pages"`
}

// Meta reads document metadata without running the conversion pipeline.
func Meta(ctx context.Context, r io.ReaderAt, size int64) (*DocumentInfo, error) {
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
	info := src.Info()

	di := &DocumentInfo{
		PageCount:  src.PageCount(),
		Title:      info.Title,
		Author:     info.Author,
		Subject:    info.Subject,
		Keywords:   info.Keywords,
		Creator:    info.Creator,
		Producer:   info.Producer,
		Language:   info.Language,
		Created:    info.Created,
		Modified:   info.Modified,
		Digest:     digest,
		Identifier: identifierFor(digest),
		Outline:    convertOutline(src.Outline()),
	}
	di.OutlineEntries = len(di.Outline)

	// Page geometry, collapsed to distinct sizes. Scanning every page of a
	// large document is cheap relative to the parse already done.
	type key struct {
		w, h float64
		rot  int
	}
	counts := map[key]int{}
	var order []key
	for i := 0; i < src.PageCount(); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := src.Page(i)
		if err != nil {
			continue
		}
		k := key{round2(p.Width), round2(p.Height), p.Rotate}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	for _, k := range order {
		di.PageSizes = append(di.PageSizes, PageSize{
			Width: k.w, Height: k.h, Rotate: k.rot, Pages: counts[k],
		})
	}
	return di, nil
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// ProbeStage names an inspectable point in the pipeline.
type ProbeStage string

const (
	// StageGlyphs dumps positioned glyphs straight out of the content stream
	// interpreter.
	StageGlyphs ProbeStage = "glyphs"
	// StageLines dumps assembled lines.
	StageLines ProbeStage = "lines"
	// StageBlocks dumps segmented blocks.
	StageBlocks ProbeStage = "blocks"
	// StageStructure dumps classified blocks with their kinds and levels.
	StageStructure ProbeStage = "structure"
)

// Valid reports whether s names a known stage.
func (s ProbeStage) Valid() bool {
	switch s {
	case StageGlyphs, StageLines, StageBlocks, StageStructure:
		return true
	}
	return false
}

// ProbeResult is the intermediate model dump, per spec principle 5.
type ProbeResult struct {
	Stage ProbeStage  `json:"stage"`
	Pages []ProbePage `json:"pages"`
	Notes []string    `json:"notes,omitempty"`
}

// ProbePage is one page of probe output.
type ProbePage struct {
	Page   int     `json:"page"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Rotate int     `json:"rotate"`

	Glyphs []ProbeGlyph `json:"glyphs,omitempty"`
	Lines  []ProbeLine  `json:"lines,omitempty"`
	Blocks []ProbeBlock `json:"blocks,omitempty"`
}

// ProbeGlyph is one positioned character.
type ProbeGlyph struct {
	Rune     string  `json:"rune"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Advance  float64 `json:"advance"`
	Size     float64 `json:"size"`
	Rise     float64 `json:"rise,omitempty"`
	Rotation float64 `json:"rotation,omitempty"`
	Font     string  `json:"font,omitempty"`
	Mode     int     `json:"render_mode,omitempty"`
	Missing  bool    `json:"missing,omitempty"`
}

// ProbeLine is one assembled line.
type ProbeLine struct {
	Text     string  `json:"text"`
	Baseline float64 `json:"baseline"`
	Bounds   Rect    `json:"bounds"`
	Size     float64 `json:"size"`
	Font     string  `json:"font,omitempty"`
	Glyphs   int     `json:"glyphs"`
}

// ProbeBlock is one segmented block with its paragraphs.
type ProbeBlock struct {
	Bounds     Rect     `json:"bounds"`
	Lines      int      `json:"lines"`
	Kind       string   `json:"kind,omitempty"`
	Level      int      `json:"level,omitempty"`
	Paragraphs []string `json:"paragraphs"`
}

// Probe runs the pipeline as far as the requested stage and returns the
// intermediate model. A page of -1 probes every selected page.
func (c *Converter) Probe(ctx context.Context, r io.ReaderAt, size int64, stage ProbeStage, page int) (*ProbeResult, error) {
	if !stage.Valid() {
		return nil, &UsageError{Err: fmt.Errorf(
			"unknown probe stage %q (want glyphs, lines, blocks, or structure)", stage)}
	}
	if size <= 0 {
		return nil, &MalformedError{Detail: "empty input"}
	}

	src, err := pdf.Open(r, size)
	if err != nil {
		return nil, translateError(err)
	}

	pages := c.selectedPages(src.PageCount())
	if page >= 0 {
		if page >= src.PageCount() {
			return nil, &UsageError{Err: fmt.Errorf(
				"page %d out of range (document has %d)", page+1, src.PageCount())}
		}
		pages = []int{page}
	}

	res := &ProbeResult{Stage: stage}
	if stage == StageStructure {
		res.Notes = append(res.Notes,
			"block kinds shown here come from the page in isolation; the body "+
				"font that spec section 4.6 measures against is a whole-document "+
				"statistic, so a full conversion may classify differently")
	}

	for _, idx := range pages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := src.Page(idx)
		if err != nil {
			continue
		}
		pp := ProbePage{
			Page: idx, Width: p.Width, Height: p.Height, Rotate: p.Rotate,
		}
		pc := src.Glyphs(p)

		if stage == StageGlyphs {
			pp.Glyphs = probeGlyphs(pc)
			res.Pages = append(res.Pages, pp)
			continue
		}

		pl := layout.AssembleLines(c.cfg, pc)
		if stage == StageLines {
			for _, l := range pl.Lines {
				pp.Lines = append(pp.Lines, ProbeLine{
					Text:     l.Text,
					Baseline: l.Baseline,
					Bounds:   toRect(l.Bounds),
					Size:     l.Size,
					Font:     fontName(l.Font),
					Glyphs:   len(l.Glyphs),
				})
			}
			res.Pages = append(res.Pages, pp)
			continue
		}

		for _, b := range layout.SegmentBlocks(c.cfg, pl.Lines) {
			pb := ProbeBlock{Bounds: toRect(b.Bounds), Lines: len(b.Lines)}
			if stage == StageStructure {
				pb.Kind = string(KindParagraph)
			}
			for _, para := range layout.Reconstruct(c.cfg, b) {
				pb.Paragraphs = append(pb.Paragraphs, para.Text)
			}
			pp.Blocks = append(pp.Blocks, pb)
		}
		res.Pages = append(res.Pages, pp)
	}
	return res, nil
}

func probeGlyphs(pc *pdf.PageContent) []ProbeGlyph {
	out := make([]ProbeGlyph, 0, len(pc.Glyphs))
	for _, g := range pc.Glyphs {
		pg := ProbeGlyph{
			Rune:     string(g.Rune),
			X:        g.X,
			Y:        g.Y,
			Advance:  g.Advance,
			Size:     g.Size,
			Rise:     g.Rise,
			Rotation: g.Rotation,
			Mode:     int(g.RenderMode),
			Missing:  g.Missing,
		}
		if int(g.FontID) < len(pc.Fonts) && g.FontID != pdf.NoFont {
			pg.Font = pc.Fonts[g.FontID].BaseFont
		}
		out = append(out, pg)
	}
	return out
}

// identifierFor exposes the EPUB identifier derivation for the meta command,
// so meta can report the identifier a conversion would produce.
func identifierFor(digest string) string { return epub.IdentifierFor(digest) }
