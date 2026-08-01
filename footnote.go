package decant

import (
	"fmt"
	"regexp"
)

// noteLabel extracts the marker a footnote body opens with, so a superscript
// in the text can be matched to it.
var noteLabel = regexp.MustCompile(`^\s*[\[(]?([0-9]{1,3}|[*†‡§¶#]{1,3})[\])]?[.\s]`)

// linkFootnotes binds superscript markers in body text to footnote blocks.
//
// Spec section 4.6 links them with epub:type="noteref" and
// epub:type="footnote", which is what makes a conforming reader show the note
// as a popup instead of stranding it at the end of a chapter.
//
// Matching is per page and by label. A marker is only linked to a note on the
// same page, because footnote numbering restarts and a "1" on page 40 is not
// the "1" on page 3.
func (c *Converter) linkFootnotes(blocks []Block, rep *Report) {
	// Index footnote blocks by page and label.
	type noteKey struct {
		page  int
		label string
	}
	notes := map[noteKey]string{}

	for i := range blocks {
		if blocks[i].Kind != KindFootnote {
			continue
		}
		m := noteLabel.FindStringSubmatch(blocks[i].Text)
		if m == nil {
			continue
		}
		key := noteKey{page: blocks[i].Page, label: m[1]}
		if _, taken := notes[key]; taken {
			// A duplicate label on one page; the first wins so the link is
			// deterministic.
			continue
		}
		notes[key] = blocks[i].ID
		blocks[i].NoteLabel = m[1]
	}
	if len(notes) == 0 {
		return
	}

	linked := 0
	for i := range blocks {
		b := &blocks[i]
		switch b.Kind {
		case KindFootnote, KindFigure, KindCode:
			continue
		}
		if len(b.Superscripts) == 0 {
			continue
		}
		for _, s := range b.Superscripts {
			id, ok := notes[noteKey{page: b.Page, label: s}]
			if !ok {
				continue
			}
			if b.NoteRefs == nil {
				b.NoteRefs = map[string]string{}
			}
			b.NoteRefs[s] = id
			linked++
		}
	}

	if linked > 0 {
		rep.info("classify", -1, fmt.Sprintf(
			"linked %d superscript marker(s) to %d footnote(s)", linked, len(notes)))
	} else if len(notes) > 0 {
		rep.info("classify", -1, fmt.Sprintf(
			"detected %d footnote(s) but found no superscript markers to link them to",
			len(notes)))
	}
}
