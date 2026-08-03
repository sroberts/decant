package pdf

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/sroberts/decant/internal/testpdf"
)

func TestOpenReadsStructure(t *testing.T) {
	data := testpdf.New().
		SetInfo("Title", "Structure Test").
		SetInfo("Author", "Someone").
		AddPage(612, 792, "").
		AddPage(595, 842, "").
		Build()

	doc, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if doc.PageCount() != 2 {
		t.Errorf("PageCount = %d, want 2", doc.PageCount())
	}

	info := doc.Info()
	if info.Title != "Structure Test" {
		t.Errorf("Title = %q", info.Title)
	}
	if info.Author != "Someone" {
		t.Errorf("Author = %q", info.Author)
	}

	p0, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if !approx(p0.Width, 612) || !approx(p0.Height, 792) {
		t.Errorf("page 0 is %vx%v, want 612x792", p0.Width, p0.Height)
	}

	p1, _ := doc.Page(1)
	if !approx(p1.Width, 595) || !approx(p1.Height, 842) {
		t.Errorf("page 1 is %vx%v, want 595x842", p1.Width, p1.Height)
	}
}

func TestPageDimensionsSwapWhenRotated(t *testing.T) {
	data := testpdf.New().AddRotatedPage(612, 792, 90, "").Build()
	doc, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if !approx(p.Width, 792) || !approx(p.Height, 612) {
		t.Errorf("rotated page is %vx%v, want 792x612", p.Width, p.Height)
	}
	if p.Rotate != 90 {
		t.Errorf("Rotate = %d, want 90", p.Rotate)
	}
}

func TestPageIndexOutOfRange(t *testing.T) {
	data := testpdf.New().AddPage(612, 792, "").Build()
	doc, _ := Open(bytes.NewReader(data), int64(len(data)))

	if _, err := doc.Page(-1); err == nil {
		t.Error("Page(-1) returned no error")
	}
	if _, err := doc.Page(5); err == nil {
		t.Error("Page(5) on a one-page document returned no error")
	}
}

func TestOutlineExtraction(t *testing.T) {
	data := testpdf.New().
		AddPage(612, 792, "").
		AddPage(612, 792, "").
		AddPage(612, 792, "").
		AddBookmark("First", 0, 700).
		AddBookmark("Second", 1, 650).
		AddBookmark("Third", 2, 600).
		Build()

	doc, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	out := doc.Outline()
	if len(out) != 3 {
		t.Fatalf("outline has %d entries, want 3: %+v", len(out), out)
	}
	for i, want := range []string{"First", "Second", "Third"} {
		if out[i].Title != want {
			t.Errorf("outline[%d].Title = %q, want %q", i, out[i].Title, want)
		}
		if out[i].Page != i {
			t.Errorf("outline[%d].Page = %d, want %d", i, out[i].Page, i)
		}
	}
	// The destination y is reported in user space, per the field docs.
	if !approx(out[0].Y, 700) {
		t.Errorf("outline[0].Y = %v, want 700", out[0].Y)
	}
}

func TestEncryptedDetection(t *testing.T) {
	data := testpdf.New().AddPage(612, 792, "").Build()
	data = bytes.Replace(data,
		[]byte("trailer\n<< /Size"),
		[]byte("trailer\n<< /Encrypt 99 0 R /Size"), 1)

	_, err := Open(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("Open accepted an encrypted PDF")
	}
	var enc *ErrEncrypted
	if !errors.As(err, &enc) {
		t.Fatalf("error is %T (%v), want *ErrEncrypted", err, err)
	}
}

func TestMalformedInputRejected(t *testing.T) {
	for _, name := range []string{"empty", "garbage", "truncated header"} {
		var data []byte
		switch name {
		case "empty":
			data = []byte{}
		case "garbage":
			data = bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 100)
		case "truncated header":
			data = []byte("%PDF-1.7\n")
		}
		if _, err := Open(bytes.NewReader(data), int64(len(data))); err == nil {
			t.Errorf("%s: Open returned no error", name)
		}
	}
}

func TestTruncatedFileIsRejectedNotPanicking(t *testing.T) {
	full := testpdf.New().
		AddPage(612, 792, "BT /F1 12 Tf 72 720 Td (Hello) Tj ET").
		Build()

	// Truncating anywhere must produce an error or a usable document, never
	// a panic.
	for _, frac := range []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.99} {
		n := int(float64(len(full)) * frac)
		data := full[:n]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %d%% truncation: %v", int(frac*100), r)
				}
			}()
			doc, err := Open(bytes.NewReader(data), int64(len(data)))
			if err != nil || doc == nil {
				return
			}
			for i := 0; i < doc.PageCount(); i++ {
				p, err := doc.Page(i)
				if err != nil {
					continue
				}
				doc.Glyphs(p)
			}
		}()
	}
}

func TestParsePDFDate(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
		zero bool
	}{
		{in: "D:20260801120000Z", want: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
		{in: "D:20260801120000", want: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
		{in: "D:20260801", want: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{in: "D:2026", want: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{in: "", zero: true},
		{in: "not a date", zero: true},
	}
	for _, c := range cases {
		got := parsePDFDate(c.in)
		if c.zero {
			if !got.IsZero() {
				t.Errorf("parsePDFDate(%q) = %v, want the zero time", c.in, got)
			}
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parsePDFDate(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDecodeTextString(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("plain ascii"), "plain ascii"},
		// UTF-16BE with a byte order mark.
		{append([]byte{0xFE, 0xFF}, 0x00, 'H', 0x00, 'i'), "Hi"},
		// UTF-16LE with a byte order mark.
		{append([]byte{0xFF, 0xFE}, 'H', 0x00, 'i', 0x00), "Hi"},
	}
	for _, c := range cases {
		if got := decodeTextString(c.in); got != c.want {
			t.Errorf("decodeTextString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// FuzzOpen asserts the xref parser never panics. Spec section 10 names
// malformed PDFs a hostile input class and requires this target alongside
// the content stream fuzzers.
func FuzzOpen(f *testing.F) {
	f.Add(testpdf.New().AddPage(612, 792, "BT /F1 12 Tf (a) Tj ET").Build())
	f.Add(testpdf.New().AddPage(612, 792, "").AddBookmark("x", 0, 0).Build())
	f.Add([]byte("%PDF-1.7\ntrailer\n<< /Size 1 /Root 1 0 R >>\nstartxref\n0\n%%EOF"))
	f.Add([]byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"))
	f.Add([]byte{})
	// The repair path in spec 4.1 is its own hostile-input surface: it scans
	// raw bytes for object markers and appends a table built from them.
	f.Add([]byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"xref\n90 5\ntrailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n60\n%%EOF\n"))
	f.Add([]byte("%PDF-1.4\r1 0 obj\r<< /Type /Catalog >>\rendobj\r"))
	f.Add([]byte("9999999999 99999 obj\n0 0 obj\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		if doc == nil {
			t.Fatal("Open returned nil document and nil error")
		}
		// Walk what was recovered; the page path must be as robust as Open.
		n := doc.PageCount()
		if n > 64 {
			n = 64
		}
		for i := 0; i < n; i++ {
			p, err := doc.Page(i)
			if err != nil {
				continue
			}
			doc.Glyphs(p)
		}
		doc.Outline()
		doc.Info()
	})
}
