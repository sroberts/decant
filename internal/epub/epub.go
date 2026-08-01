// Package epub serializes an EPUB 3.3 container with an EPUB 2 NCX fallback.
// It is stage 8 of the pipeline in spec section 4.
package epub

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

// Book is everything needed to serialize one EPUB.
type Book struct {
	// Identifier is the dc:identifier value, normally a urn:uuid produced by
	// UUIDv5 over the input file's SHA-256.
	Identifier string
	Title      string
	Authors    []string
	// Language is a BCP 47 tag. Defaults to "en" when empty.
	Language string
	// Source records the original filename, emitted as dc:source.
	Source string
	// Modified fills dcterms:modified and every ZIP header timestamp.
	Modified time.Time

	Chapters []Chapter
	// Nav is the table of contents. When empty, one entry per chapter is
	// synthesized from chapter titles.
	Nav []NavPoint

	// CSS is the stylesheet body. Empty omits the stylesheet entirely, which
	// the minimal device profile in spec section 5 relies on.
	CSS string

	// NavDepth caps the TOC depth, per the device profile table in spec
	// section 5. Zero means unlimited.
	NavDepth int
}

// Chapter is one XHTML document in the spine.
type Chapter struct {
	// ID is the manifest id and the basename of the file, e.g. "ch001".
	ID string
	// Title fills the <title> element.
	Title string
	// Body is a well-formed XHTML fragment placed inside <body>.
	Body string
}

// NavPoint is one table-of-contents entry.
type NavPoint struct {
	Title string
	// Href is relative to the OEBPS directory, e.g. "text/ch001.xhtml#h3".
	Href     string
	Children []NavPoint
}

const (
	contentDir = "OEBPS"
	cssPath    = "styles/base.css"
)

// Write serializes the book. Identical input produces byte-identical output.
func Write(w io.Writer, b *Book) error {
	if len(b.Chapters) == 0 {
		return fmt.Errorf("epub: no chapters to write")
	}

	lang := b.Language
	if lang == "" {
		lang = "en"
	}
	nav := b.Nav
	if len(nav) == 0 {
		nav = navFromChapters(b.Chapters)
	}
	if b.NavDepth > 0 {
		nav = truncateNav(nav, b.NavDepth)
	}

	entries := []zipEntry{
		// The mimetype entry must be first and stored uncompressed.
		{name: "mimetype", data: []byte("application/epub+zip"), store: true},
		{name: "META-INF/container.xml", data: []byte(containerXML)},
		{name: contentDir + "/package.opf", data: []byte(buildOPF(b, lang))},
		{name: contentDir + "/nav.xhtml", data: []byte(buildNav(b, lang, nav))},
		{name: contentDir + "/toc.ncx", data: []byte(buildNCX(b, nav))},
	}
	if b.CSS != "" {
		entries = append(entries, zipEntry{
			name: contentDir + "/" + cssPath,
			data: []byte(b.CSS),
		})
	}
	for _, ch := range b.Chapters {
		entries = append(entries, zipEntry{
			name: fmt.Sprintf("%s/text/%s.xhtml", contentDir, ch.ID),
			data: []byte(wrapXHTML(ch, lang, b.CSS != "")),
		})
	}

	return deterministicZip(w, entries, b.Modified)
}

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/package.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

// wrapXHTML renders a chapter body into a complete XHTML document.
func wrapXHTML(ch Chapter, lang string, withCSS bool) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<!DOCTYPE html>` + "\n")
	fmt.Fprintf(&sb,
		`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="%s" lang="%s">`+"\n",
		esc(lang), esc(lang))
	sb.WriteString("<head>\n")
	sb.WriteString(`  <meta charset="utf-8"/>` + "\n")
	fmt.Fprintf(&sb, "  <title>%s</title>\n", esc(ch.Title))
	if withCSS {
		fmt.Fprintf(&sb,
			`  <link rel="stylesheet" type="text/css" href="../%s"/>`+"\n", cssPath)
	}
	sb.WriteString("</head>\n<body>\n")
	sb.WriteString(ch.Body)
	if !strings.HasSuffix(ch.Body, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("</body>\n</html>\n")
	return sb.String()
}

// buildOPF renders the package document.
func buildOPF(b *Book, lang string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&sb,
		`<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="pub-id" xml:lang="%s">`+"\n",
		esc(lang))

	sb.WriteString(`  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` + "\n")
	fmt.Fprintf(&sb, "    <dc:identifier id=\"pub-id\">%s</dc:identifier>\n", esc(b.Identifier))
	fmt.Fprintf(&sb, "    <dc:title>%s</dc:title>\n", esc(b.Title))
	fmt.Fprintf(&sb, "    <dc:language>%s</dc:language>\n", esc(lang))
	for i, a := range b.Authors {
		id := fmt.Sprintf("creator%d", i+1)
		fmt.Fprintf(&sb, "    <dc:creator id=\"%s\">%s</dc:creator>\n", id, esc(a))
		fmt.Fprintf(&sb,
			"    <meta refines=\"#%s\" property=\"role\" scheme=\"marc:relators\">aut</meta>\n", id)
	}
	if b.Source != "" {
		fmt.Fprintf(&sb, "    <dc:source>%s</dc:source>\n", esc(b.Source))
	}
	fmt.Fprintf(&sb, "    <meta property=\"dcterms:modified\">%s</meta>\n",
		b.Modified.UTC().Format("2006-01-02T15:04:05Z"))
	sb.WriteString("  </metadata>\n")

	sb.WriteString("  <manifest>\n")
	sb.WriteString(`    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n")
	sb.WriteString(`    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n")
	if b.CSS != "" {
		fmt.Fprintf(&sb,
			`    <item id="css" href="%s" media-type="text/css"/>`+"\n", cssPath)
	}
	for _, ch := range b.Chapters {
		fmt.Fprintf(&sb,
			`    <item id="%s" href="text/%s.xhtml" media-type="application/xhtml+xml"/>`+"\n",
			esc(ch.ID), esc(ch.ID))
	}
	sb.WriteString("  </manifest>\n")

	sb.WriteString(`  <spine toc="ncx">` + "\n")
	for _, ch := range b.Chapters {
		fmt.Fprintf(&sb, `    <itemref idref="%s"/>`+"\n", esc(ch.ID))
	}
	sb.WriteString("  </spine>\n")
	sb.WriteString("</package>\n")
	return sb.String()
}

// buildNav renders the EPUB 3 navigation document, with both the toc and
// landmarks nav elements.
func buildNav(b *Book, lang string, nav []NavPoint) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<!DOCTYPE html>` + "\n")
	fmt.Fprintf(&sb,
		`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="%s" lang="%s">`+"\n",
		esc(lang), esc(lang))
	sb.WriteString("<head>\n  <meta charset=\"utf-8\"/>\n")
	fmt.Fprintf(&sb, "  <title>%s</title>\n", esc(b.Title))
	sb.WriteString("</head>\n<body>\n")

	sb.WriteString(`  <nav epub:type="toc" id="toc">` + "\n")
	sb.WriteString("    <h1>Table of Contents</h1>\n")
	writeNavList(&sb, nav, 2)
	sb.WriteString("  </nav>\n")

	sb.WriteString(`  <nav epub:type="landmarks" id="landmarks" hidden="hidden">` + "\n")
	sb.WriteString("    <h1>Landmarks</h1>\n    <ol>\n")
	fmt.Fprintf(&sb,
		`      <li><a epub:type="bodymatter" href="text/%s.xhtml">Start of Content</a></li>`+"\n",
		esc(b.Chapters[0].ID))
	sb.WriteString("    </ol>\n  </nav>\n")

	sb.WriteString("</body>\n</html>\n")
	return sb.String()
}

func writeNavList(sb *strings.Builder, points []NavPoint, depth int) {
	if len(points) == 0 {
		return
	}
	ind := strings.Repeat("  ", depth)
	fmt.Fprintf(sb, "%s<ol>\n", ind)
	for _, p := range points {
		fmt.Fprintf(sb, "%s  <li><a href=\"%s\">%s</a>", ind, esc(p.Href), esc(p.Title))
		if len(p.Children) > 0 {
			sb.WriteString("\n")
			writeNavList(sb, p.Children, depth+2)
			fmt.Fprintf(sb, "%s  </li>\n", ind)
		} else {
			sb.WriteString("</li>\n")
		}
	}
	fmt.Fprintf(sb, "%s</ol>\n", ind)
}

// buildNCX renders the EPUB 2 fallback table of contents. Every device
// profile in spec section 5 keeps it.
func buildNCX(b *Book, nav []NavPoint) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">` + "\n")
	sb.WriteString("  <head>\n")
	fmt.Fprintf(&sb, `    <meta name="dtb:uid" content="%s"/>`+"\n", esc(b.Identifier))
	fmt.Fprintf(&sb, `    <meta name="dtb:depth" content="%d"/>`+"\n", navDepth(nav))
	sb.WriteString(`    <meta name="dtb:totalPageCount" content="0"/>` + "\n")
	sb.WriteString(`    <meta name="dtb:maxPageNumber" content="0"/>` + "\n")
	sb.WriteString("  </head>\n")
	fmt.Fprintf(&sb, "  <docTitle><text>%s</text></docTitle>\n", esc(b.Title))
	if len(b.Authors) > 0 {
		fmt.Fprintf(&sb, "  <docAuthor><text>%s</text></docAuthor>\n", esc(b.Authors[0]))
	}

	sb.WriteString("  <navMap>\n")
	order := 0
	writeNavPoints(&sb, nav, &order, 2)
	sb.WriteString("  </navMap>\n")
	sb.WriteString("</ncx>\n")
	return sb.String()
}

func writeNavPoints(sb *strings.Builder, points []NavPoint, order *int, depth int) {
	ind := strings.Repeat("  ", depth)
	for _, p := range points {
		*order++
		n := *order
		fmt.Fprintf(sb, "%s<navPoint id=\"navPoint-%d\" playOrder=\"%d\">\n", ind, n, n)
		fmt.Fprintf(sb, "%s  <navLabel><text>%s</text></navLabel>\n", ind, esc(p.Title))
		fmt.Fprintf(sb, "%s  <content src=\"%s\"/>\n", ind, esc(p.Href))
		writeNavPoints(sb, p.Children, order, depth+1)
		fmt.Fprintf(sb, "%s</navPoint>\n", ind)
	}
}

func navDepth(points []NavPoint) int {
	best := 0
	for _, p := range points {
		d := 1 + navDepth(p.Children)
		if d > best {
			best = d
		}
	}
	if best == 0 {
		return 1
	}
	return best
}

func truncateNav(points []NavPoint, depth int) []NavPoint {
	if depth <= 0 {
		return nil
	}
	out := make([]NavPoint, 0, len(points))
	for _, p := range points {
		p.Children = truncateNav(p.Children, depth-1)
		out = append(out, p)
	}
	return out
}

func navFromChapters(chs []Chapter) []NavPoint {
	out := make([]NavPoint, 0, len(chs))
	for _, ch := range chs {
		t := ch.Title
		if t == "" {
			t = ch.ID
		}
		out = append(out, NavPoint{Title: t, Href: "text/" + ch.ID + ".xhtml"})
	}
	return out
}

// esc escapes text for XML content and double-quoted attribute values. It
// covers both contexts so callers do not have to choose.
var escaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func esc(s string) string { return escaper.Replace(stripInvalidXML(s)) }

// EscapeXML escapes text for XML content and double-quoted attribute values,
// and drops the control characters XML 1.0 forbids. PDF metadata and extracted
// text both carry those routinely, and epubcheck rejects them.
func EscapeXML(s string) string { return esc(s) }

// stripInvalidXML removes control characters XML 1.0 forbids. PDF metadata
// routinely carries them and epubcheck rejects the result.
func stripInvalidXML(s string) string {
	if !strings.ContainsFunc(s, invalidXMLRune) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if invalidXMLRune(r) {
			return -1
		}
		return r
	}, s)
}

func invalidXMLRune(r rune) bool {
	switch {
	case r == 0x09 || r == 0x0A || r == 0x0D:
		return false
	case r < 0x20:
		return true
	case r >= 0xD800 && r <= 0xDFFF:
		return true
	case r == 0xFFFE || r == 0xFFFF:
		return true
	}
	return false
}

// UUIDv5 computes an RFC 4122 version 5 UUID over a namespace and name.
func UUIDv5(namespace [16]byte, name string) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)

	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = (u[6] & 0x0F) | 0x50 // version 5
	u[8] = (u[8] & 0x3F) | 0x80 // RFC 4122 variant

	var sb strings.Builder
	sb.Grow(36)
	hexs := hex.EncodeToString(u[:])
	sb.WriteString(hexs[0:8])
	sb.WriteByte('-')
	sb.WriteString(hexs[8:12])
	sb.WriteByte('-')
	sb.WriteString(hexs[12:16])
	sb.WriteByte('-')
	sb.WriteString(hexs[16:20])
	sb.WriteByte('-')
	sb.WriteString(hexs[20:32])
	return sb.String()
}

// NamespaceURL is the RFC 4122 URL namespace UUID,
// 6ba7b811-9dad-11d1-80b4-00c04fd430c8.
var NamespaceURL = [16]byte{
	0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1,
	0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8,
}

// identifierPrefix namespaces the digest inside the URL namespace, so decant
// identifiers cannot collide with a v5 UUID some other tool derives from the
// same digest.
const identifierPrefix = "https://github.com/sroberts/decant/pdf-sha256/"

// IdentifierFor returns the urn:uuid identifier for an input file digest.
// Spec section 4.9 makes this a UUIDv5 over the input's SHA-256 so that
// reconverting the same file yields the same identifier.
func IdentifierFor(sha256Hex string) string {
	return "urn:uuid:" + UUIDv5(NamespaceURL, identifierPrefix+sha256Hex)
}
