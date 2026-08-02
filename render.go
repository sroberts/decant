package decant

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sroberts/decant/internal/epub"
	"github.com/sroberts/decant/internal/layout"
)

// buildChapters groups blocks into chapter files and renders their XHTML.
//
// Grouping happens twice, per spec section 4.9: first at the --split-at
// boundary, then again at --max-chunk-bytes. Both splits land on a paragraph
// boundary, and the second appends -2, -3 suffixes to the chapter ID.
func (c *Converter) buildChapters(doc *Document, imgs *imageSet, rep *Report) ([]epub.Chapter, []epub.NavPoint) {
	groups := c.groupBlocks(doc.Blocks)
	if len(groups) == 0 {
		return nil, nil
	}

	var chapters []epub.Chapter
	// headingRefs collects every heading in document order with the file it
	// landed in, which the hierarchical TOC is built from afterward.
	var headingRefs []headingRef

	// Splitting has to finish before any body is rendered, because a
	// cross-reference needs the file its target landed in and that target may
	// sit in a chapter not yet built. Spec 4.9.
	type chapterPart struct {
		id, title, href string
		first           bool
		blocks          []Block
	}
	var parts []chapterPart
	for gi, g := range groups {
		title := chapterTitle(g, gi, doc)
		baseID := fmt.Sprintf("ch%03d", gi+1)
		for pi, part := range c.splitBySize(g, rep, baseID) {
			id := baseID
			if pi > 0 {
				id = fmt.Sprintf("%s-%d", baseID, pi+1)
			}
			parts = append(parts, chapterPart{
				id: id, title: title, href: "text/" + id + ".xhtml",
				first: pi == 0, blocks: part,
			})
		}
	}

	// Which file each block landed in, and which blocks a cross-reference
	// actually points at. Only the latter get an id attribute: anchoring
	// every paragraph would inflate each file against the crosspoint chunk
	// budget for no benefit.
	fileOf := map[string]string{}
	for _, p := range parts {
		for _, b := range p.blocks {
			fileOf[b.ID] = p.id + ".xhtml"
		}
	}
	targeted := map[string]bool{}
	for _, b := range doc.Blocks {
		for _, ref := range b.Links {
			if ref.TargetID != "" {
				targeted[ref.TargetID] = true
			}
		}
	}

	for _, p := range parts {
		lc := &linkContext{fileOf: fileOf, targeted: targeted, self: p.id + ".xhtml"}

		chapters = append(chapters, epub.Chapter{
			ID:    p.id,
			Title: p.title,
			Body:  c.renderBlocks(p.blocks, imgs, rep, lc),
		})

		for _, b := range p.blocks {
			if b.Kind != KindHeading || strings.TrimSpace(b.Text) == "" {
				continue
			}
			headingRefs = append(headingRefs, headingRef{
				title: layout.StripSuperscriptMarks(b.Text),
				level: b.Level,
				href:  p.href + "#" + b.ID,
			})
		}
		// A chapter that contributes no heading still needs an entry, or it
		// would be unreachable from the table of contents.
		if p.first && !partHasHeading(p.blocks) {
			headingRefs = append(headingRefs, headingRef{
				title: p.title,
				level: 1,
				href:  p.href,
			})
		}
	}

	return chapters, buildNav(headingRefs)
}

// idAttr returns an id attribute for a block that something links to, and
// nothing otherwise.
func idAttr(lc *linkContext, id string) string {
	if !lc.anchored(id) {
		return ""
	}
	return fmt.Sprintf(` id="%s"`, epub.EscapeXML(id))
}

// linkContext is what the renderer needs to emit cross-references: where each
// block landed, which blocks are pointed at, and which file is being written.
type linkContext struct {
	fileOf   map[string]string
	targeted map[string]bool
	self     string
}

// href returns the link target for a block ID, relative to the file being
// rendered. A target in the same file is a bare fragment, which keeps the
// common case short.
func (lc *linkContext) href(id string) (string, bool) {
	if lc == nil || id == "" {
		return "", false
	}
	file, ok := lc.fileOf[id]
	if !ok {
		return "", false
	}
	if file == lc.self {
		return "#" + id, true
	}
	return file + "#" + id, true
}

// anchored reports whether a block needs an id attribute emitted.
func (lc *linkContext) anchored(id string) bool {
	return lc != nil && lc.targeted[id]
}

// headingRef is one heading with the file and anchor it serializes to.
type headingRef struct {
	title string
	level int
	href  string
}

func partHasHeading(blocks []Block) bool {
	for _, b := range blocks {
		if b.Kind == KindHeading && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
}

// buildNav turns a flat, document-ordered heading list into the nested TOC
// spec section 4.9 calls for.
//
// Levels in real documents skip ranks: an h1 followed directly by an h3 is
// common. Each heading nests under the closest preceding heading of a lower
// level, which keeps the tree well formed without inventing empty entries.
func buildNav(refs []headingRef) []epub.NavPoint {
	if len(refs) == 0 {
		return nil
	}

	// The tree is built from individually allocated nodes rather than nested
	// slices. Holding pointers into a slice while appending to it would let a
	// reallocation silently strand every child added through the stale
	// pointer.
	type node struct {
		title    string
		href     string
		level    int
		children []*node
	}

	var roots []*node
	var stack []*node

	for _, r := range refs {
		lv := r.level
		if lv < 1 {
			lv = 1
		}
		n := &node{title: r.title, href: r.href, level: lv}

		// Unwind to the nearest ancestor with a strictly lower level.
		for len(stack) > 0 && stack[len(stack)-1].level >= lv {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			roots = append(roots, n)
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, n)
		}
		stack = append(stack, n)
	}

	var convert func([]*node) []epub.NavPoint
	convert = func(ns []*node) []epub.NavPoint {
		if len(ns) == 0 {
			return nil
		}
		out := make([]epub.NavPoint, 0, len(ns))
		for _, n := range ns {
			out = append(out, epub.NavPoint{
				Title:    n.title,
				Href:     n.href,
				Children: convert(n.children),
			})
		}
		return out
	}
	return convert(roots)
}

// groupBlocks applies the --split-at boundary.
func (c *Converter) groupBlocks(blocks []Block) [][]Block {
	if len(blocks) == 0 {
		return nil
	}

	switch c.opts.SplitAt {
	case SplitAtNone:
		return [][]Block{blocks}

	case SplitAtPage:
		var groups [][]Block
		var cur []Block
		page := blocks[0].Page
		for _, b := range blocks {
			if b.Page != page && len(cur) > 0 {
				groups = append(groups, cur)
				cur = nil
			}
			page = b.Page
			cur = append(cur, b)
		}
		if len(cur) > 0 {
			groups = append(groups, cur)
		}
		return groups

	default:
		// h1 or h2. Heading classification is M2; until then no block is a
		// heading and this yields a single chapter, which is the M1 shape.
		level := 1
		if c.opts.SplitAt == SplitAtH2 {
			level = 2
		}
		var groups [][]Block
		var cur []Block
		for _, b := range blocks {
			isBoundary := b.Kind == KindHeading && b.Level > 0 && b.Level <= level
			if isBoundary && len(cur) > 0 {
				groups = append(groups, cur)
				cur = nil
			}
			cur = append(cur, b)
		}
		if len(cur) > 0 {
			groups = append(groups, cur)
		}
		return groups
	}
}

// splitBySize breaks a chapter that would exceed MaxChunkBytes, always at a
// block boundary so a paragraph is never cut in half.
func (c *Converter) splitBySize(blocks []Block, rep *Report, chapterID string) [][]Block {
	limit := c.opts.MaxChunkBytes
	if limit <= 0 {
		return [][]Block{blocks}
	}

	// The wrapper adds a fixed XHTML preamble; budget for it so the written
	// file, not just the body, stays under the limit.
	const wrapperOverhead = 512
	budget := limit - wrapperOverhead
	if budget < 1024 {
		budget = 1024
	}

	var parts [][]Block
	var cur []Block
	size := 0

	for _, b := range blocks {
		n := renderedSize(b)
		if n > budget && len(cur) == 0 {
			// A single block larger than the whole budget. Emitting it alone
			// is the only option that keeps paragraphs intact.
			rep.warn("assemble", b.Page, fmt.Sprintf(
				"chapter %s contains a %d byte block, above the %d byte chunk limit; "+
					"it was not split because that would break a paragraph",
				chapterID, n, limit))
			parts = append(parts, []Block{b})
			continue
		}
		if size+n > budget && len(cur) > 0 {
			parts = append(parts, cur)
			cur = nil
			size = 0
		}
		cur = append(cur, b)
		size += n
	}
	if len(cur) > 0 {
		parts = append(parts, cur)
	}
	if len(parts) > 1 {
		rep.info("assemble", -1, fmt.Sprintf(
			"chapter %s split into %d files to stay under the %d byte chunk limit",
			chapterID, len(parts), limit))
	}
	return parts
}

// renderedSize estimates a block's serialized length. Escaping can only grow
// the text, so the estimate is scaled to stay conservative.
func renderedSize(b Block) int {
	if b.Kind == KindFigure {
		// The image itself is a separate container entry; only the figure
		// markup lands in the XHTML.
		return len(b.Text)*12/10 + 160
	}
	return len(b.Text)*12/10 + 32
}

// renderBlocks renders a chapter body as an XHTML fragment.
//
// imgs resolves a figure's ImageID to its dimensions and href; a figure whose
// image is missing renders as its caption alone rather than a broken link.
func (c *Converter) renderBlocks(blocks []Block, imgs *imageSet, rep *Report, lc *linkContext) string {
	var sb strings.Builder
	sb.Grow(len(blocks) * 96)

	// first marks the opening paragraph of a section, which the stylesheet
	// leaves un-indented.
	first := true

	for _, b := range blocks {
		text := renderInline(b, lc)
		if strings.TrimSpace(text) == "" && b.Kind != KindFigure && b.Kind != KindTable {
			continue
		}
		// A heading boundary must not be taken from a non-heading kind.

		switch b.Kind {
		case KindFigure:
			img, ok := imgs.byID(b.ImageID)
			if !ok {
				// The image was dropped after the block was made. Keep the
				// caption, which is real content, and drop the frame.
				if text != "" {
					fmt.Fprintf(&sb, "<p class=\"caption\"%s>%s</p>\n", idAttr(lc, b.ID), text)
					first = true
				}
				continue
			}
			alt := text
			if alt == "" {
				alt = "Figure"
			}
			if b.InlineImage {
				// Spec 4.7 flows a narrow image inside a paragraph rather
				// than breaking the text around it.
				fmt.Fprintf(&sb, "<p%s><img src=\"../%s\" alt=\"%s\"/></p>\n",
					idAttr(lc, b.ID), epub.EscapeXML(img.Href()), epub.EscapeXML(alt))
			} else if text != "" {
				fmt.Fprintf(&sb,
					"<figure id=\"%s\">\n  <img src=\"../%s\" alt=\"%s\"/>\n  <figcaption>%s</figcaption>\n</figure>\n",
					epub.EscapeXML(b.ID), epub.EscapeXML(img.Href()),
					epub.EscapeXML(alt), text)
			} else {
				fmt.Fprintf(&sb,
					"<figure id=\"%s\">\n  <img src=\"../%s\" alt=\"%s\"/>\n</figure>\n",
					epub.EscapeXML(b.ID), epub.EscapeXML(img.Href()),
					epub.EscapeXML(alt))
			}
			first = true

		case KindHeading:
			level := b.Level
			if level < 1 {
				level = 1
			}
			if level > 6 {
				level = 6
			}
			fmt.Fprintf(&sb, "<h%d id=\"%s\">%s</h%d>\n",
				level, epub.EscapeXML(b.ID), text, level)
			first = true

		case KindCode:
			// Spec 4.6 preserves leading whitespace in code, which the
			// stylesheet's pre-wrap honors.
			fmt.Fprintf(&sb, "<pre%s><code>%s</code></pre>\n", idAttr(lc, b.ID), text)
			first = true

		case KindQuote:
			fmt.Fprintf(&sb, "<blockquote%s><p>%s</p></blockquote>\n", idAttr(lc, b.ID), text)
			first = true

		case KindList:
			renderList(&sb, b, idAttr(lc, b.ID))
			first = true

		case KindTable:
			c.renderTable(&sb, b, rep)
			first = true

		case KindFootnote:
			// epub:type footnote is what makes a conforming reader show this
			// as a popup rather than a block of text at the page foot.
			fmt.Fprintf(&sb,
				"<aside epub:type=\"footnote\" id=\"%s\">\n  <p>%s</p>\n</aside>\n",
				epub.EscapeXML(b.ID), text)
			first = true

		case KindCaption:
			fmt.Fprintf(&sb, "<p class=\"caption\"%s>%s</p>\n", idAttr(lc, b.ID), text)
			first = true

		default:
			// Paragraph. An anchor is emitted only when a cross-reference
			// actually points here: anchoring every paragraph would inflate
			// every file against the crosspoint chunk budget for no benefit.
			// Spec 4.9.
			class := ""
			if first {
				class = ` class="first"`
				first = false
			}
			fmt.Fprintf(&sb, "<p%s%s>%s</p>\n", class, idAttr(lc, b.ID), text)
		}
	}
	return sb.String()
}

// renderInline escapes a block's text and turns superscript runs into markup.
//
// Escaping happens per segment so the emitted tags survive: escaping the
// whole string first would turn the angle brackets of <sup> into entities.
// A superscript with a matching footnote becomes a noteref anchor, which is
// what makes a conforming reader show the note as a popup.
func renderInline(b Block, lc *linkContext) string {
	if spans := resolvableRefs(b, lc); len(spans) > 0 {
		return renderWithCrossRefs(b, lc, spans)
	}
	return renderRuns(b, b.Text, true)
}

// resolvableRefs returns the block's cross-references that have a target in
// the output, ordered and clipped to the text.
func resolvableRefs(b Block, lc *linkContext) []CrossRef {
	if lc == nil || len(b.Links) == 0 {
		return nil
	}
	var out []CrossRef
	for _, ref := range b.Links {
		if ref.TargetID == "" {
			continue
		}
		if _, ok := lc.href(ref.TargetID); !ok {
			continue
		}
		if ref.Start < 0 || ref.End > len(b.Text) || ref.Start >= ref.End {
			continue
		}
		out = append(out, ref)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })

	// Overlaps were resolved during mapping, but a caller may have edited the
	// block tree between Analyze and Write. Nested anchors are invalid XHTML,
	// so drop rather than trust.
	kept := out[:0]
	prevEnd := 0
	for _, ref := range out {
		if ref.Start < prevEnd {
			continue
		}
		kept = append(kept, ref)
		prevEnd = ref.End
	}
	return kept
}

// renderWithCrossRefs wraps each linked range in an anchor, rendering the
// segments between them normally.
//
// Superscript noterefs inside a linked range emit as plain sup rather than as
// their own anchor, because an anchor inside an anchor is invalid XHTML. A
// footnote marker that falls inside a cross-reference is rare and losing its
// link is the lesser damage.
func renderWithCrossRefs(b Block, lc *linkContext, spans []CrossRef) string {
	var sb strings.Builder
	pos := 0
	for _, ref := range spans {
		sb.WriteString(renderRuns(b, b.Text[pos:ref.Start], true))
		href, _ := lc.href(ref.TargetID)
		fmt.Fprintf(&sb, `<a href="%s">%s</a>`,
			epub.EscapeXML(href), renderRuns(b, b.Text[ref.Start:ref.End], false))
		pos = ref.End
	}
	sb.WriteString(renderRuns(b, b.Text[pos:], true))
	return sb.String()
}

// renderRuns escapes text and turns superscript runs into sup elements,
// linking them to their footnote when noteRefs is set.
func renderRuns(b Block, raw string, noteRefs bool) string {
	if !strings.Contains(raw, layout.SuperscriptOpen) {
		return epub.EscapeXML(raw)
	}

	var sb strings.Builder
	rest := raw
	for {
		i := strings.Index(rest, layout.SuperscriptOpen)
		if i < 0 {
			sb.WriteString(epub.EscapeXML(rest))
			break
		}
		sb.WriteString(epub.EscapeXML(rest[:i]))
		rest = rest[i+len(layout.SuperscriptOpen):]

		j := strings.Index(rest, layout.SuperscriptClose)
		if j < 0 {
			// Unterminated run; emit what remains as plain text.
			sb.WriteString(epub.EscapeXML(rest))
			break
		}
		label := rest[:j]
		rest = rest[j+len(layout.SuperscriptClose):]

		escaped := epub.EscapeXML(label)
		if id, ok := b.NoteRefs[strings.TrimSpace(label)]; ok && noteRefs {
			fmt.Fprintf(&sb,
				`<a epub:type="noteref" href="#%s"><sup>%s</sup></a>`,
				epub.EscapeXML(id), escaped)
			continue
		}
		fmt.Fprintf(&sb, "<sup>%s</sup>", escaped)
	}
	return sb.String()
}

// renderTable emits a table in whichever form --table-mode selects.
//
// Spec section 4.8 makes auto depend on confidence: a real table only when
// both detection signals fired, a rasterized region at medium, and
// space-preserved text at low. Rasterizing needs the vector renderer that
// spec section 13 keeps open, so image mode degrades to text and says so
// rather than silently emitting something the caller did not ask for.
func (c *Converter) renderTable(sb *strings.Builder, b Block, rep *Report) {
	mode := c.opts.Tables
	if mode == TableAuto {
		switch b.TableConfidence {
		case string(layout.ConfidenceHigh):
			mode = TableHTML
		case string(layout.ConfidenceMedium):
			mode = TableImage
		default:
			mode = TableText
		}
	}
	if mode == TableImage {
		rep.warnOnce("tables",
			"table rasterization needs the vector renderer that spec section 13 "+
				"keeps open, so tables that would be rasterized are emitted as "+
				"space-preserved text instead")
		mode = TableText
	}

	switch mode {
	case TableDrop:
		return
	case TableHTML:
		renderTableHTML(sb, b)
	default:
		renderTableText(sb, b)
	}
}

func renderTableHTML(sb *strings.Builder, b Block) {
	fmt.Fprintf(sb, "<table id=\"%s\">\n", epub.EscapeXML(b.ID))
	for i, row := range b.TableRows {
		sb.WriteString("  <tr>\n")
		// The first row of a ruled table is its header often enough, and a
		// th costs nothing when it is not.
		tag := "td"
		if i == 0 {
			tag = "th"
		}
		for _, cell := range row {
			span := ""
			if cell.ColSpan > 1 {
				span = fmt.Sprintf(" colspan=\"%d\"", cell.ColSpan)
			}
			fmt.Fprintf(sb, "    <%s%s>%s</%s>\n",
				tag, span, epub.EscapeXML(cell.Text), tag)
		}
		sb.WriteString("  </tr>\n")
	}
	sb.WriteString("</table>\n")
}

// renderTableText emits the table as aligned, space-preserved text.
//
// Columns are padded to a common width so the shape survives in a reader that
// cannot lay out a table, which is the point of the fallback.
func renderTableText(sb *strings.Builder, b Block) {
	widths := map[int]int{}
	for _, row := range b.TableRows {
		for j, c := range row {
			if n := len([]rune(c.Text)); n > widths[j] {
				widths[j] = n
			}
		}
	}

	var text strings.Builder
	for i, row := range b.TableRows {
		if i > 0 {
			text.WriteByte('\n')
		}
		for j, c := range row {
			if j > 0 {
				text.WriteString("  ")
			}
			text.WriteString(c.Text)
			if j < len(row)-1 {
				for k := len([]rune(c.Text)); k < widths[j]; k++ {
					text.WriteByte(' ')
				}
			}
		}
	}
	fmt.Fprintf(sb, "<pre id=\"%s\">%s</pre>\n",
		epub.EscapeXML(b.ID), epub.EscapeXML(text.String()))
}

// renderList emits a list block as ul or ol.
//
// Spec section 4.6 infers the start attribute from the first marker, so a
// list resuming at 7 after an interruption keeps its numbering.
func renderList(sb *strings.Builder, b Block, id string) {
	items := b.ListItems
	if len(items) == 0 {
		items = strings.Split(b.Text, "\n")
	}

	if b.ListOrdered {
		if b.ListStart > 1 {
			fmt.Fprintf(sb, "<ol%s start=\"%d\">\n", id, b.ListStart)
		} else {
			fmt.Fprintf(sb, "<ol%s>\n", id)
		}
	} else {
		fmt.Fprintf(sb, "<ul%s>\n", id)
	}
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		fmt.Fprintf(sb, "  <li>%s</li>\n",
			epub.EscapeXML(layout.StripSuperscriptMarks(it)))
	}
	if b.ListOrdered {
		sb.WriteString("</ol>\n")
	} else {
		sb.WriteString("</ul>\n")
	}
}

// chapterTitle picks a display title for a chapter group.
func chapterTitle(blocks []Block, index int, doc *Document) string {
	for _, b := range blocks {
		if b.Kind == KindHeading && strings.TrimSpace(b.Text) != "" {
			return layout.StripSuperscriptMarks(b.Text)
		}
	}
	if index == 0 && doc.Title != "" {
		return doc.Title
	}
	if len(blocks) > 0 {
		return fmt.Sprintf("Page %d", blocks[0].Page+1)
	}
	return fmt.Sprintf("Section %d", index+1)
}
