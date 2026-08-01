// Package testpdf builds small, valid PDFs in memory for tests.
//
// The golden corpus in spec section 10 is supplied at build time from real
// documents. This package covers the gap underneath it: deterministic
// fixtures that exercise specific interpreter and layout behavior without
// depending on any file on disk.
package testpdf

import (
	"bytes"
	"fmt"
	"strings"
)

// Builder assembles a PDF document.
type Builder struct {
	pages []page
	info  map[string]string
	// fonts maps a resource name to a font dictionary body, without the
	// enclosing angle brackets.
	fonts map[string]string
	// outline holds top-level bookmarks pointing at page indices.
	outline []bookmark
	// xobjects holds image XObjects available to every page.
	xobjects map[string]xobject
}

// xobject is an uncompressed 8-bit RGB image.
type xobject struct {
	width, height int
	rgb           []byte
}

type page struct {
	width, height float64
	rotate        int
	content       string
}

type bookmark struct {
	title string
	page  int
	y     float64
	// child, when set, is a single nested bookmark under this one. One level
	// is enough to exercise outline depth handling.
	child *bookmark
}

// New returns a Builder with Helvetica available as /F1.
func New() *Builder {
	return &Builder{
		info: map[string]string{},
		fonts: map[string]string{
			"F1": "/Type /Font /Subtype /Type1 /BaseFont /Helvetica",
			"F2": "/Type /Font /Subtype /Type1 /BaseFont /Times-Roman",
			"F3": "/Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold",
			"F4": "/Type /Font /Subtype /Type1 /BaseFont /Courier",
		},
	}
}

// SetInfo sets an /Info dictionary entry, e.g. Title or Author.
func (b *Builder) SetInfo(key, value string) *Builder {
	b.info[key] = value
	return b
}

// AddFont registers an extra font resource. The body is the dictionary
// content without the enclosing angle brackets.
func (b *Builder) AddFont(name, body string) *Builder {
	b.fonts[name] = body
	return b
}

// AddPage appends a page carrying the given raw content stream.
func (b *Builder) AddPage(width, height float64, content string) *Builder {
	b.pages = append(b.pages, page{width: width, height: height, content: content})
	return b
}

// AddRotatedPage appends a page with a /Rotate value.
func (b *Builder) AddRotatedPage(width, height float64, rotate int, content string) *Builder {
	b.pages = append(b.pages, page{
		width: width, height: height, rotate: rotate, content: content,
	})
	return b
}

// AddBookmark adds a top-level outline entry pointing at a page.
func (b *Builder) AddBookmark(title string, pageIndex int, y float64) *Builder {
	b.outline = append(b.outline, bookmark{title: title, page: pageIndex, y: y})
	return b
}

// AddNestedBookmark adds a top-level outline entry with one child under it,
// which exercises outline depth mapping onto heading levels.
func (b *Builder) AddNestedBookmark(title string, page int, y float64, childTitle string, childPage int, childY float64) *Builder {
	b.outline = append(b.outline, bookmark{
		title: title, page: page, y: y,
		child: &bookmark{title: childTitle, page: childPage, y: childY},
	})
	return b
}

// TextPage is a convenience helper: it lays out lines of text down the page
// in one font at one size, starting at (x, top) in PDF user space and
// stepping down by leading.
func TextPage(font string, size, x, top, leading float64, lines []string) string {
	var sb strings.Builder
	sb.WriteString("BT\n")
	fmt.Fprintf(&sb, "/%s %g Tf\n", font, size)
	fmt.Fprintf(&sb, "%g TL\n", leading)
	fmt.Fprintf(&sb, "1 0 0 1 %g %g Tm\n", x, top)
	for i, l := range lines {
		if i > 0 {
			sb.WriteString("T*\n")
		}
		fmt.Fprintf(&sb, "(%s) Tj\n", escapeString(l))
	}
	sb.WriteString("ET\n")
	return sb.String()
}

// RotatedTextPage lays out text pre-rotated 90 degrees counter-clockwise in
// user space, which is what a real /Rotate 90 page does so that its text
// reads horizontally once the viewer applies the page rotation.
//
// Text drawn with plain TextPage on a /Rotate 90 page genuinely displays
// running top to bottom, and decant is right to treat that as a rotated run.
func RotatedTextPage(font string, size, x, top, leading float64, lines []string) string {
	var sb strings.Builder
	sb.WriteString("BT\n")
	fmt.Fprintf(&sb, "/%s %g Tf\n", font, size)
	fmt.Fprintf(&sb, "%g TL\n", leading)
	// A +90 degree rotation: [cos sin -sin cos] = [0 1 -1 0].
	fmt.Fprintf(&sb, "0 1 -1 0 %g %g Tm\n", x, top)
	for i, l := range lines {
		if i > 0 {
			sb.WriteString("T*\n")
		}
		fmt.Fprintf(&sb, "(%s) Tj\n", escapeString(l))
	}
	sb.WriteString("ET\n")
	return sb.String()
}

// TwoColumnPage lays out two columns of text side by side, optionally under a
// full-width heading that spans both.
//
// Column geometry mirrors a typical academic paper: a gutter roughly two em
// wide between columns of equal measure.
func TwoColumnPage(font string, size, leading float64, heading string, left, right []string) string {
	var sb strings.Builder
	sb.WriteString("BT\n")
	fmt.Fprintf(&sb, "/%s %g Tf\n", font, size)
	fmt.Fprintf(&sb, "%g TL\n", leading)

	top := 720.0
	if heading != "" {
		// The heading is one continuous run across the whole measure, so it
		// carries no inter-glyph gap at the gutter and must survive splitting.
		fmt.Fprintf(&sb, "1 0 0 1 %g %g Tm\n", 72.0, top)
		fmt.Fprintf(&sb, "(%s) Tj\n", escapeString(heading))
		top -= leading * 2
	}

	const leftX, rightX = 72.0, 320.0
	for _, col := range []struct {
		x     float64
		lines []string
	}{{leftX, left}, {rightX, right}} {
		fmt.Fprintf(&sb, "1 0 0 1 %g %g Tm\n", col.x, top)
		for i, l := range col.lines {
			if i > 0 {
				sb.WriteString("T*\n")
			}
			fmt.Fprintf(&sb, "(%s) Tj\n", escapeString(l))
		}
	}
	sb.WriteString("ET\n")
	return sb.String()
}

// HeadingPage lays out a document with headings at larger sizes interleaved
// with body text, which is what structure classification keys on.
//
// Each section is a heading line at headingSize followed by its body lines at
// bodySize, in the same font.
func HeadingPage(font string, bodySize, headingSize, leading float64, sections [][]string) string {
	return HeadingPageAt(font, bodySize, headingSize, leading, 720, sections)
}

// HeadingPageAt is HeadingPage with an explicit starting baseline in user
// space, so several calls can stack down one page without overlapping.
func HeadingPageAt(font string, bodySize, headingSize, leading, startY float64, sections [][]string) string {
	var sb strings.Builder
	sb.WriteString("BT\n")
	y := startY

	for _, sec := range sections {
		if len(sec) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "/%s %g Tf\n", font, headingSize)
		fmt.Fprintf(&sb, "1 0 0 1 %g %g Tm\n", 72.0, y)
		fmt.Fprintf(&sb, "(%s) Tj\n", escapeString(sec[0]))
		y -= headingSize * 1.6

		fmt.Fprintf(&sb, "/%s %g Tf\n", font, bodySize)
		fmt.Fprintf(&sb, "%g TL\n", leading)
		fmt.Fprintf(&sb, "1 0 0 1 %g %g Tm\n", 72.0, y)
		for i, l := range sec[1:] {
			if i > 0 {
				sb.WriteString("T*\n")
			}
			fmt.Fprintf(&sb, "(%s) Tj\n", escapeString(l))
			y -= leading
		}
		y -= leading
	}
	sb.WriteString("ET\n")
	return sb.String()
}

// AddImage registers an image XObject built from raw 8-bit RGB samples.
//
// The samples are stored uncompressed, which keeps the fixture readable and
// exercises the same /DeviceRGB decode path a Flate-encoded image takes.
func (b *Builder) AddImage(name string, width, height int, rgb []byte) *Builder {
	if b.xobjects == nil {
		b.xobjects = map[string]xobject{}
	}
	b.xobjects[name] = xobject{width: width, height: height, rgb: rgb}
	return b
}

// SolidRGB builds uncompressed samples for a solid colour.
func SolidRGB(width, height int, r, g, bl byte) []byte {
	out := make([]byte, 0, width*height*3)
	for i := 0; i < width*height; i++ {
		out = append(out, r, g, bl)
	}
	return out
}

// GradientRGB builds samples with enough distinct colours to exceed the
// 256-colour line-art threshold, so the encoder chooses JPEG.
func GradientRGB(width, height int) []byte {
	out := make([]byte, 0, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			out = append(out,
				byte((x*7+y*3)%256),
				byte((x*13+y*11)%256),
				byte((x*29+y*17)%256))
		}
	}
	return out
}

// DrawImage returns the content stream operators that paint a registered
// image at (x, y) in user space at the given size.
func DrawImage(name string, x, y, w, h float64) string {
	return fmt.Sprintf("q\n%g 0 0 %g %g %g cm\n/%s Do\nQ\n", w, h, x, y, name)
}

// escapeString escapes a Go string for a PDF literal string operand.
func escapeString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`)
	return r.Replace(s)
}

// Build serializes the document. The output is a classic cross-reference
// table PDF, which keeps the fixture readable when a test fails.
func (b *Builder) Build() []byte {
	if len(b.pages) == 0 {
		b.AddPage(612, 792, "")
	}

	// Object numbering:
	//   1                catalog
	//   2                page tree
	//   3 .. 3+n-1       page dicts
	//   then, per page   content stream
	//   then             font dicts
	//   then             info, outline root, outline items
	const catalogNr = 1
	const pagesNr = 2
	firstPageNr := 3
	firstContentNr := firstPageNr + len(b.pages)

	fontNames := sortedKeys(b.fonts)
	firstFontNr := firstContentNr + len(b.pages)

	xobjNames := make([]string, 0, len(b.xobjects))
	for k := range b.xobjects {
		xobjNames = append(xobjNames, k)
	}
	for i := 1; i < len(xobjNames); i++ {
		for j := i; j > 0 && xobjNames[j] < xobjNames[j-1]; j-- {
			xobjNames[j], xobjNames[j-1] = xobjNames[j-1], xobjNames[j]
		}
	}
	firstXObjNr := firstFontNr + len(fontNames)
	infoNr := firstXObjNr + len(xobjNames)
	outlineRootNr := infoNr + 1
	firstOutlineNr := outlineRootNr + 1

	// Outline objects: one per top-level bookmark, plus one per child.
	type onode struct {
		bm    bookmark
		objNr int
		child int // object number of the nested bookmark, 0 when none
	}
	var onodes []onode
	nextObj := firstOutlineNr
	for _, bm := range b.outline {
		n := onode{bm: bm, objNr: nextObj}
		nextObj++
		if bm.child != nil {
			n.child = nextObj
			nextObj++
		}
		onodes = append(onodes, n)
	}

	totalObjs := infoNr
	if len(b.outline) > 0 {
		totalObjs = nextObj - 1
	}

	var buf bytes.Buffer
	offsets := make([]int, totalObjs+1)

	buf.WriteString("%PDF-1.7\n")
	// A binary comment marks the file as containing binary data, which some
	// readers use to pick a transfer mode.
	buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})

	obj := func(nr int, body string) {
		offsets[nr] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", nr, body)
	}

	// Catalog.
	catalog := fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R", pagesNr)
	if len(b.outline) > 0 {
		catalog += fmt.Sprintf(" /Outlines %d 0 R", outlineRootNr)
	}
	catalog += " >>"
	obj(catalogNr, catalog)

	// Page tree.
	var kids strings.Builder
	kids.WriteString("[")
	for i := range b.pages {
		if i > 0 {
			kids.WriteString(" ")
		}
		fmt.Fprintf(&kids, "%d 0 R", firstPageNr+i)
	}
	kids.WriteString("]")
	obj(pagesNr, fmt.Sprintf("<< /Type /Pages /Kids %s /Count %d >>",
		kids.String(), len(b.pages)))

	// Font resource dictionary, shared by every page.
	var res strings.Builder
	res.WriteString("<< /Font << ")
	for i, name := range fontNames {
		fmt.Fprintf(&res, "/%s %d 0 R ", name, firstFontNr+i)
	}
	res.WriteString(">>")
	if len(xobjNames) > 0 {
		res.WriteString(" /XObject << ")
		for i, name := range xobjNames {
			fmt.Fprintf(&res, "/%s %d 0 R ", name, firstXObjNr+i)
		}
		res.WriteString(">>")
	}
	res.WriteString(" >>")

	// Page dictionaries.
	for i, p := range b.pages {
		body := fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %g %g] /Resources %s /Contents %d 0 R",
			pagesNr, p.width, p.height, res.String(), firstContentNr+i)
		if p.rotate != 0 {
			body += fmt.Sprintf(" /Rotate %d", p.rotate)
		}
		body += " >>"
		obj(firstPageNr+i, body)
	}

	// Content streams.
	for i, p := range b.pages {
		body := fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream",
			len(p.content), p.content)
		obj(firstContentNr+i, body)
	}

	// Fonts.
	for i, name := range fontNames {
		obj(firstFontNr+i, "<< "+b.fonts[name]+" >>")
	}

	// Image XObjects, stored uncompressed.
	for i, name := range xobjNames {
		x := b.xobjects[name]
		offsets[firstXObjNr+i] = buf.Len()
		fmt.Fprintf(&buf,
			"%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
				"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Length %d >>\nstream\n",
			firstXObjNr+i, x.width, x.height, len(x.rgb))
		buf.Write(x.rgb)
		buf.WriteString("\nendstream\nendobj\n")
	}

	// Info.
	var info strings.Builder
	info.WriteString("<<")
	for _, k := range sortedKeys(b.info) {
		fmt.Fprintf(&info, " /%s (%s)", k, escapeString(b.info[k]))
	}
	info.WriteString(" >>")
	obj(infoNr, info.String())

	// Outline.
	if len(onodes) > 0 {
		obj(outlineRootNr, fmt.Sprintf(
			"<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>",
			onodes[0].objNr, onodes[len(onodes)-1].objNr, len(onodes)))

		for i, n := range onodes {
			pageRef := firstPageNr + n.bm.page
			body := fmt.Sprintf(
				"<< /Title (%s) /Parent %d 0 R /Dest [%d 0 R /XYZ 0 %g 0]",
				escapeString(n.bm.title), outlineRootNr, pageRef, n.bm.y)
			if i > 0 {
				body += fmt.Sprintf(" /Prev %d 0 R", onodes[i-1].objNr)
			}
			if i < len(onodes)-1 {
				body += fmt.Sprintf(" /Next %d 0 R", onodes[i+1].objNr)
			}
			if n.child != 0 {
				body += fmt.Sprintf(" /First %d 0 R /Last %d 0 R /Count 1", n.child, n.child)
			}
			body += " >>"
			obj(n.objNr, body)

			if n.child != 0 {
				c := n.bm.child
				obj(n.child, fmt.Sprintf(
					"<< /Title (%s) /Parent %d 0 R /Dest [%d 0 R /XYZ 0 %g 0] >>",
					escapeString(c.title), n.objNr, firstPageNr+c.page, c.y))
			}
		}
	}

	// Cross-reference table.
	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", totalObjs+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= totalObjs; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}

	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\n",
		totalObjs+1, catalogNr, infoNr)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)

	return buf.Bytes()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Insertion sort keeps this dependency-free and the sets are tiny.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
