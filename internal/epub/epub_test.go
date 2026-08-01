package epub

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func sampleBook() *Book {
	return &Book{
		Identifier: "urn:uuid:00000000-0000-5000-8000-000000000000",
		Title:      "Sample",
		Authors:    []string{"An Author"},
		Language:   "en",
		Source:     "sample.pdf",
		Modified:   testTime,
		CSS:        BaseCSS,
		Chapters: []Chapter{
			{ID: "ch001", Title: "One", Body: "<p>First chapter.</p>"},
			{ID: "ch002", Title: "Two", Body: "<p>Second chapter.</p>"},
		},
	}
}

func writeBook(t *testing.T, b *Book) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, b); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func read(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("opening archive: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", name, err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b)
	}
	t.Fatalf("no entry named %s", name)
	return ""
}

func TestWriteIsDeterministic(t *testing.T) {
	a := writeBook(t, sampleBook())
	b := writeBook(t, sampleBook())
	if !bytes.Equal(a, b) {
		t.Error("two writes of identical input produced different bytes")
	}
}

func TestEntryOrderAndCompression(t *testing.T) {
	data := writeBook(t, sampleBook())
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("opening archive: %v", err)
	}

	if zr.File[0].Name != "mimetype" {
		t.Fatalf("first entry is %q, want mimetype", zr.File[0].Name)
	}
	if zr.File[0].Method != zip.Store {
		t.Error("mimetype is compressed; the EPUB spec requires it stored")
	}

	// Every remaining entry sorts by name, which is what makes the output
	// independent of map iteration order.
	var rest []string
	for _, f := range zr.File[1:] {
		rest = append(rest, f.Name)
	}
	for i := 1; i < len(rest); i++ {
		if rest[i-1] > rest[i] {
			t.Errorf("entries are not sorted: %q before %q", rest[i-1], rest[i])
		}
	}
}

func TestNoExtraFieldsAndFixedTimestamps(t *testing.T) {
	data := writeBook(t, sampleBook())
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))

	for _, f := range zr.File {
		if len(f.Extra) != 0 {
			t.Errorf("%s carries %d bytes of extra field, want 0", f.Name, len(f.Extra))
		}
		if f.Modified.UTC() != testTime {
			t.Errorf("%s timestamp = %v, want %v", f.Name, f.Modified.UTC(), testTime)
		}
	}
}

func TestTimestampChangesOutput(t *testing.T) {
	// Determinism must come from the fixed timestamp, not from ignoring it.
	b1 := sampleBook()
	b2 := sampleBook()
	b2.Modified = testTime.Add(48 * time.Hour)

	if bytes.Equal(writeBook(t, b1), writeBook(t, b2)) {
		t.Error("changing the timestamp did not change the output")
	}
}

func TestMSDOSTimeConversion(t *testing.T) {
	cases := []struct {
		in         time.Time
		wantYear   int
		wantMonth  time.Month
		wantDay    int
		wantHour   int
		wantMinute int
	}{
		{in: testTime, wantYear: 2026, wantMonth: 8, wantDay: 1, wantHour: 12},
		// Out-of-range times clamp to the MS-DOS epoch rather than wrapping.
		{in: time.Unix(0, 0).UTC(), wantYear: 1980, wantMonth: 1, wantDay: 1},
		{in: time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC), wantYear: 1980, wantMonth: 1, wantDay: 1},
	}
	for _, c := range cases {
		date, tm := toMSDOS(c.in)
		year := int(date>>9) + 1980
		month := time.Month((date >> 5) & 0x0F)
		day := int(date & 0x1F)
		hour := int(tm >> 11)
		minute := int((tm >> 5) & 0x3F)

		if year != c.wantYear || month != c.wantMonth || day != c.wantDay {
			t.Errorf("toMSDOS(%v) date = %d-%02d-%02d, want %d-%02d-%02d",
				c.in, year, month, day, c.wantYear, c.wantMonth, c.wantDay)
		}
		if c.wantHour != 0 && (hour != c.wantHour || minute != c.wantMinute) {
			t.Errorf("toMSDOS(%v) time = %02d:%02d, want %02d:%02d",
				c.in, hour, minute, c.wantHour, c.wantMinute)
		}
	}
}

func TestPackageDocument(t *testing.T) {
	data := writeBook(t, sampleBook())
	opf := read(t, data, "OEBPS/package.opf")

	for _, want := range []string{
		`<dc:identifier id="pub-id">urn:uuid:00000000-0000-5000-8000-000000000000</dc:identifier>`,
		"<dc:title>Sample</dc:title>",
		"<dc:language>en</dc:language>",
		"<dc:source>sample.pdf</dc:source>",
		`<meta property="dcterms:modified">2026-08-01T12:00:00Z</meta>`,
		`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`,
		`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`,
		`<spine toc="ncx">`,
		`<itemref idref="ch001"/>`,
		`<itemref idref="ch002"/>`,
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("package.opf missing %q\ngot:\n%s", want, opf)
		}
	}
}

func TestNavAndNCX(t *testing.T) {
	data := writeBook(t, sampleBook())

	nav := read(t, data, "OEBPS/nav.xhtml")
	if !strings.Contains(nav, `epub:type="toc"`) {
		t.Error("nav.xhtml has no toc nav element")
	}
	if !strings.Contains(nav, `epub:type="landmarks"`) {
		t.Error("nav.xhtml has no landmarks nav element")
	}
	if !strings.Contains(nav, `href="text/ch001.xhtml"`) {
		t.Error("nav.xhtml does not link the first chapter")
	}

	ncx := read(t, data, "OEBPS/toc.ncx")
	if !strings.Contains(ncx, `<navPoint id="navPoint-1" playOrder="1">`) {
		t.Error("toc.ncx has no first navPoint")
	}
	if !strings.Contains(ncx, "<text>One</text>") {
		t.Error("toc.ncx is missing the first chapter label")
	}
}

func TestNestedNavAndDepthCap(t *testing.T) {
	b := sampleBook()
	b.Nav = []NavPoint{{
		Title: "Part One",
		Href:  "text/ch001.xhtml",
		Children: []NavPoint{{
			Title: "Chapter A",
			Href:  "text/ch001.xhtml#a",
			Children: []NavPoint{
				{Title: "Section i", Href: "text/ch001.xhtml#i"},
			},
		}},
	}}

	full := read(t, writeBook(t, b), "OEBPS/nav.xhtml")
	if !strings.Contains(full, "Section i") {
		t.Error("uncapped nav lost the third level")
	}

	// The crosspoint and minimal profiles flatten to two levels, because
	// deep nesting is unusable on a two-button device.
	b.NavDepth = 2
	capped := read(t, writeBook(t, b), "OEBPS/nav.xhtml")
	if strings.Contains(capped, "Section i") {
		t.Error("NavDepth 2 did not drop the third level")
	}
	if !strings.Contains(capped, "Chapter A") {
		t.Error("NavDepth 2 dropped the second level too")
	}
}

func TestChapterWrapping(t *testing.T) {
	data := writeBook(t, sampleBook())
	ch := read(t, data, "OEBPS/text/ch001.xhtml")

	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<!DOCTYPE html>",
		`xmlns="http://www.w3.org/1999/xhtml"`,
		"<title>One</title>",
		`href="../styles/base.css"`,
		"<p>First chapter.</p>",
	} {
		if !strings.Contains(ch, want) {
			t.Errorf("chapter missing %q\ngot:\n%s", want, ch)
		}
	}
}

func TestNoStylesheetWhenCSSEmpty(t *testing.T) {
	b := sampleBook()
	b.CSS = ""
	data := writeBook(t, b)

	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	for _, f := range zr.File {
		if strings.Contains(f.Name, "base.css") {
			t.Error("a stylesheet was written despite empty CSS")
		}
	}
	if strings.Contains(read(t, data, "OEBPS/text/ch001.xhtml"), "stylesheet") {
		t.Error("chapter links a stylesheet that was not written")
	}
	if strings.Contains(read(t, data, "OEBPS/package.opf"), "text/css") {
		t.Error("manifest lists a stylesheet that was not written")
	}
}

func TestEscaping(t *testing.T) {
	b := sampleBook()
	b.Title = `Tom & Jerry's <Adventure> "Quoted"`
	data := writeBook(t, b)
	opf := read(t, data, "OEBPS/package.opf")

	if strings.Contains(opf, "Tom & Jerry") {
		t.Error("a bare ampersand survived into the XML")
	}
	if !strings.Contains(opf, "Tom &amp; Jerry&apos;s &lt;Adventure&gt; &quot;Quoted&quot;") {
		t.Errorf("title was not escaped correctly:\n%s", opf)
	}
}

func TestControlCharactersStripped(t *testing.T) {
	b := sampleBook()
	// PDF metadata routinely carries control characters that XML 1.0 forbids.
	b.Title = "Bad\x00Title\x01Here\x08"
	data := writeBook(t, b)
	opf := read(t, data, "OEBPS/package.opf")

	if strings.ContainsAny(opf, "\x00\x01\x08") {
		t.Error("control characters survived into the XML")
	}
	if !strings.Contains(opf, "BadTitleHere") {
		t.Errorf("stripping removed too much:\n%s", opf)
	}
	// Tab, newline, and carriage return are legal and must survive.
	if got := esc("a\tb\nc"); got != "a\tb\nc" {
		t.Errorf("esc stripped legal whitespace: %q", got)
	}
}

func TestUUIDv5(t *testing.T) {
	// RFC 4122 appendix worked example: v5 over the DNS namespace and
	// "www.example.org".
	dns := [16]byte{
		0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1,
		0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8,
	}
	got := UUIDv5(dns, "www.example.org")
	const want = "74738ff5-5367-5958-9aee-98fffdcd1876"
	if got != want {
		t.Errorf("UUIDv5 = %q, want %q", got, want)
	}
}

func TestUUIDv5FormatAndVariant(t *testing.T) {
	u := UUIDv5(NamespaceURL, "anything")
	if len(u) != 36 {
		t.Fatalf("UUID %q has length %d, want 36", u, len(u))
	}
	// Version nibble.
	if u[14] != '5' {
		t.Errorf("version nibble = %c, want 5 (in %q)", u[14], u)
	}
	// Variant: the high bits of octet 8 must be 10xx, i.e. 8, 9, a, or b.
	switch u[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Errorf("variant nibble = %c, want 8, 9, a, or b (in %q)", u[19], u)
	}
}

func TestIdentifierForIsStableAndDistinct(t *testing.T) {
	a := IdentifierFor("abc123")
	if a != IdentifierFor("abc123") {
		t.Error("IdentifierFor is not deterministic")
	}
	if a == IdentifierFor("abc124") {
		t.Error("different digests produced the same identifier")
	}
	if !strings.HasPrefix(a, "urn:uuid:") {
		t.Errorf("identifier %q lacks the urn:uuid prefix", a)
	}
}

func TestWriteRejectsEmptyBook(t *testing.T) {
	b := sampleBook()
	b.Chapters = nil
	if err := Write(io.Discard, b); err == nil {
		t.Error("Write accepted a book with no chapters")
	}
}

func TestLanguageDefaults(t *testing.T) {
	b := sampleBook()
	b.Language = ""
	opf := read(t, writeBook(t, b), "OEBPS/package.opf")
	if !strings.Contains(opf, "<dc:language>en</dc:language>") {
		t.Error("empty language did not default to en")
	}
}

func TestNavSynthesizedFromChapters(t *testing.T) {
	b := sampleBook()
	b.Nav = nil
	nav := read(t, writeBook(t, b), "OEBPS/nav.xhtml")
	if !strings.Contains(nav, ">One</a>") || !strings.Contains(nav, ">Two</a>") {
		t.Errorf("nav was not synthesized from chapter titles:\n%s", nav)
	}
}

func TestBaseCSSStaysUnderFiftyLines(t *testing.T) {
	// Spec section 4.9 caps the stylesheet at 50 lines; CrossPoint attributes
	// out-of-memory crashes to complex CSS.
	if n := strings.Count(BaseCSS, "\n"); n > 50 {
		t.Errorf("BaseCSS is %d lines, above the 50 line cap", n)
	}
	if n := strings.Count(MinimalCSS, "\n"); n > 50 {
		t.Errorf("MinimalCSS is %d lines, above the 50 line cap", n)
	}
	// No embedded fonts and no fixed dimensions, so reader defaults win.
	// A relative max-width on images is required rather than forbidden.
	for _, css := range []string{BaseCSS, MinimalCSS} {
		for _, forbidden := range []string{"@font-face", "px", "pt;", "@import"} {
			if strings.Contains(css, forbidden) {
				t.Errorf("stylesheet contains %q; reader defaults should win", forbidden)
			}
		}
		if !strings.Contains(css, "max-width: 100%") {
			t.Error("stylesheet does not constrain images to the reading width")
		}
	}
}
