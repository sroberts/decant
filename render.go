package decant

import (
	"fmt"
	"strings"

	"github.com/sroberts/decant/internal/epub"
)

// buildChapters groups blocks into chapter files and renders their XHTML.
//
// Grouping happens twice, per spec section 4.9: first at the --split-at
// boundary, then again at --max-chunk-bytes. Both splits land on a paragraph
// boundary, and the second appends -2, -3 suffixes to the chapter ID.
func (c *Converter) buildChapters(doc *Document, rep *Report) ([]epub.Chapter, []epub.NavPoint) {
	groups := c.groupBlocks(doc.Blocks)
	if len(groups) == 0 {
		return nil, nil
	}

	var chapters []epub.Chapter
	var nav []epub.NavPoint

	for gi, g := range groups {
		title := chapterTitle(g, gi, doc)
		baseID := fmt.Sprintf("ch%03d", gi+1)

		parts := c.splitBySize(g, rep, baseID)
		for pi, part := range parts {
			id := baseID
			if pi > 0 {
				id = fmt.Sprintf("%s-%d", baseID, pi+1)
			}
			chapters = append(chapters, epub.Chapter{
				ID:    id,
				Title: title,
				Body:  renderBlocks(part),
			})
			// Only the first part of a split chapter earns a TOC entry;
			// continuation files are the same chapter.
			if pi == 0 {
				nav = append(nav, epub.NavPoint{
					Title: title,
					Href:  "text/" + id + ".xhtml",
				})
			}
		}
	}
	return chapters, nav
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
	return len(b.Text)*12/10 + 32
}

// renderBlocks renders a chapter body as an XHTML fragment.
func renderBlocks(blocks []Block) string {
	var sb strings.Builder
	sb.Grow(len(blocks) * 96)

	// first marks the opening paragraph of a section, which the stylesheet
	// leaves un-indented.
	first := true

	for _, b := range blocks {
		text := epub.EscapeXML(b.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}

		switch b.Kind {
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
			fmt.Fprintf(&sb, "<pre><code>%s</code></pre>\n", text)
			first = true

		case KindQuote:
			fmt.Fprintf(&sb, "<blockquote><p>%s</p></blockquote>\n", text)
			first = true

		case KindCaption:
			fmt.Fprintf(&sb, "<p class=\"caption\">%s</p>\n", text)
			first = true

		default:
			// Paragraph. Anchor IDs are emitted only on headings for now;
			// paragraph anchors arrive with internal cross-reference
			// rewriting, and emitting them universally would inflate every
			// file against the crosspoint chunk budget.
			if first {
				fmt.Fprintf(&sb, "<p class=\"first\">%s</p>\n", text)
				first = false
			} else {
				fmt.Fprintf(&sb, "<p>%s</p>\n", text)
			}
		}
	}
	return sb.String()
}

// chapterTitle picks a display title for a chapter group.
func chapterTitle(blocks []Block, index int, doc *Document) string {
	for _, b := range blocks {
		if b.Kind == KindHeading && strings.TrimSpace(b.Text) != "" {
			return b.Text
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
