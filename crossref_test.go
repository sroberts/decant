package decant_test

import (
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// --- internal cross-references, spec 4.9 ---

// linkDoc builds a two-page document whose first page carries a cross-
// reference pointing at the heading on the second.
//
// The referring words sit on a line of their own so the annotation rectangle
// can be derived from the line's baseline rather than from a guess at glyph
// pitch. TextPage advances by the font's real widths, so any rectangle
// computed from an assumed pitch lands a rune or two off; the precise
// glyph-to-offset mapping is covered by unit tests in internal/layout.
func linkDoc() []byte {
	const (
		size    = 11.0
		left    = 72.0
		top     = 720.0
		leading = 14.0
	)
	page1 := testpdf.TextPage("F1", size, left, top, leading, []string{
		"An opening paragraph that refers to",
		"Chapter Two",
		"for the details, and then continues with more prose.",
	})
	page2 := testpdf.HeadingPageAt("F1", size, 18, leading, 720, [][]string{
		{"Chapter Two", "The body of the second chapter, running to a second line here."},
	})

	// The second line's baseline, in user space, with a generous horizontal
	// span: the rectangle only has to select that line's glyphs.
	baseline := top - leading
	return testpdf.New().
		AddPage(612, 792, page1).
		AddLink(0, baseline-2, 612, baseline+size, 1, 730).
		AddPage(612, 792, page2).
		Build()
}

func linkedBlocks(doc *decant.Document) []decant.Block {
	var out []decant.Block
	for _, b := range doc.Blocks {
		if len(b.Links) > 0 {
			out = append(out, b)
		}
	}
	return out
}

func TestCrossReferenceIsDetected(t *testing.T) {
	doc := analyze(t, linkDoc(), defaultOpts())

	linked := linkedBlocks(doc)
	if len(linked) != 1 {
		t.Fatalf("%d blocks carry a link, want 1\n%s", len(linked), blockTexts(doc))
	}
	ref := linked[0].Links[0]
	if got := linked[0].Text[ref.Start:ref.End]; got != "Chapter Two" {
		t.Errorf("link covers %q, want %q (block %q)", got, "Chapter Two", linked[0].Text)
	}
	if ref.TargetID == "" {
		t.Error("the link resolved to no target block")
	}
}

func TestCrossReferenceResolvesToTheHeading(t *testing.T) {
	// The destination names a point on page two; spec 4.9 rewrites it against
	// the anchor of the block that point lands on, which is the heading.
	doc := analyze(t, linkDoc(), defaultOpts())

	linked := linkedBlocks(doc)
	if len(linked) == 0 {
		t.Fatal("no linked block")
	}
	target := linked[0].Links[0].TargetID

	for _, b := range doc.Blocks {
		if b.ID != target {
			continue
		}
		if b.Kind != decant.KindHeading {
			t.Errorf("link resolved to a %s (%q), want the heading", b.Kind, b.Text)
		}
		if !strings.Contains(b.Text, "Chapter Two") {
			t.Errorf("link resolved to %q, want the Chapter Two heading", b.Text)
		}
		return
	}
	t.Errorf("target %q matches no block", target)
}

func TestCrossReferenceRendersAsAnAnchor(t *testing.T) {
	xhtml := chapterXHTML(t, linkDoc(), defaultOpts())

	if !strings.Contains(xhtml, "<a href=") {
		t.Fatalf("no anchor emitted:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, ">Chapter Two</a>") {
		t.Errorf("the anchor does not wrap the referring words:\n%s", xhtml)
	}
	// The surrounding prose must survive intact.
	if !strings.Contains(xhtml, "refers to") || !strings.Contains(xhtml, "details") {
		t.Errorf("text around the link was damaged:\n%s", xhtml)
	}
}

func TestCrossReferenceTargetIsAnchored(t *testing.T) {
	// A dangling fragment is an epubcheck error, so every href must have an
	// id to land on.
	out, _ := buildDoc(t, linkDoc(), defaultOpts())
	xhtml := allChapterText(t, out)

	for _, href := range hrefFragments(xhtml) {
		if !strings.Contains(xhtml, `id="`+href+`"`) {
			t.Errorf("href #%s has no matching id\n%s", href, xhtml)
		}
	}
}

func TestUnanchoredBlocksStayUnanchored(t *testing.T) {
	// Anchoring every paragraph would inflate every file against the
	// crosspoint chunk budget, so only blocks something points at get an id.
	plain := chapterXHTML(t, simpleDoc(), defaultOpts())
	if strings.Contains(plain, "<p id=") {
		t.Errorf("a document with no links emitted paragraph anchors:\n%s", plain)
	}
}

func TestCrossReferencesAreDeterministic(t *testing.T) {
	src := linkDoc()
	first, _ := buildDoc(t, src, defaultOpts())
	second, _ := buildDoc(t, src, defaultOpts())
	if !bytesEqual(first, second) {
		t.Error("two conversions of a linked document differ byte for byte")
	}
}

func TestCallerCanSuppressACrossReference(t *testing.T) {
	// The block tree is editable between Analyze and Write; clearing a
	// target must drop the anchor rather than emit a dangling href.
	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatal(err)
	}
	doc := analyze(t, linkDoc(), defaultOpts())
	for i := range doc.Blocks {
		for j := range doc.Blocks[i].Links {
			doc.Blocks[i].Links[j].TargetID = ""
		}
	}

	var out strings.Builder
	if _, err := conv.Write(contextTODO(), doc, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<a href=") {
		t.Error("clearing the target left an anchor behind")
	}
}

// hrefFragments returns the fragment identifiers every same-document href
// points at.
func hrefFragments(s string) []string {
	var out []string
	rest := s
	for {
		i := strings.Index(rest, `<a href="`)
		if i < 0 {
			return out
		}
		rest = rest[i+len(`<a href="`):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return out
		}
		href := rest[:j]
		rest = rest[j:]
		if k := strings.Index(href, "#"); k >= 0 {
			if frag := href[k+1:]; frag != "" {
				out = append(out, frag)
			}
		}
	}
}
