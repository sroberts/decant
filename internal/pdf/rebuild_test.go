package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// A minimal but valid PDF, built by hand so the test can damage exactly one
// thing at a time. testpdf cannot be used here: it imports this package.
func minimalPDF() []byte {
	var b strings.Builder
	offsets := map[int]int{}
	obj := func(n int, body string) {
		offsets[n] = b.Len()
		b.WriteString("\n")
		offsets[n] = b.Len()
		b.WriteString(itoaPDF(n) + " 0 obj\n" + body + "\nendobj\n")
	}
	b.WriteString("%PDF-1.4\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>")
	obj(4, "<< /Length 0 >>\nstream\n\nendstream")

	start := b.Len()
	b.WriteString("xref\n0 5\n0000000000 65535 f \n")
	for n := 1; n <= 4; n++ {
		b.WriteString(pad10(offsets[n]) + " 00000 n \n")
	}
	b.WriteString("trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n" +
		itoaPDF(start) + "\n%%EOF\n")
	return []byte(b.String())
}

func itoaPDF(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func pad10(n int) string {
	s := itoaPDF(n)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

func TestMinimalFixtureOpens(t *testing.T) {
	// The fixture has to be good before damaging it proves anything.
	data := minimalPDF()
	doc, err := open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("the undamaged fixture does not open: %v", err)
	}
	if doc.PageCount() != 1 {
		t.Errorf("PageCount = %d, want 1", doc.PageCount())
	}
}

// damagedXref returns the fixture with its cross-reference subsection
// declaring the wrong object range, so every entry in it indexes the wrong
// object number and nothing resolves.
//
// This is the shape of the corpus's unreadablemetadata.pdf, where a trailer
// declaring /Size 175 sits above a subsection covering objects 175 through
// 266. It is also the damage pdfcpu does not recover from by itself: a
// missing table, a missing trailer, and a startxref pointing nowhere are all
// handled upstream, which is worth knowing before assuming a rebuild is what
// rescued a file.
func damagedXref() []byte {
	return bytes.Replace(minimalPDF(), []byte("xref\n0 5\n"), []byte("xref\n90 5\n"), 1)
}

func TestDamagedXrefFailsWithoutRepair(t *testing.T) {
	data := damagedXref()
	if _, err := open(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("the damaged fixture opened without repair; it is not damaged")
	}
}

func TestOpenRebuildsADamagedXref(t *testing.T) {
	data := damagedXref()
	doc, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open did not recover a damaged xref: %v", err)
	}
	if !doc.Repaired {
		t.Error("the document opened but Repaired is false")
	}
	if doc.PageCount() != 1 {
		t.Errorf("PageCount = %d after repair, want 1", doc.PageCount())
	}
}

func TestRepairIsNotClaimedOnAHealthyFile(t *testing.T) {
	// A rebuilt index is a weaker claim about a file than one it declared
	// itself, so the flag must not be set when nothing was wrong.
	data := minimalPDF()
	doc, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Repaired {
		t.Error("Repaired is set on a file that opened normally")
	}
}

func TestRebuildFindsTheCatalogWithoutATrailer(t *testing.T) {
	// With the trailer gone there is no /Root to read, so the catalog has to
	// be found by scanning object bodies.
	data := minimalPDF()
	i := bytes.Index(data, []byte("trailer"))
	if i < 0 {
		t.Fatal("fixture has no trailer")
	}
	truncated := data[:i]

	out, err := rebuildXref(bytes.NewReader(truncated), int64(len(truncated)))
	if err != nil {
		t.Fatalf("rebuildXref: %v", err)
	}
	if !bytes.Contains(out, []byte("/Root 1 0 R")) {
		t.Errorf("the rebuilt trailer does not name the catalog:\n%s",
			out[len(out)-160:])
	}
	doc, err := open(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("the rebuilt document does not open: %v", err)
	}
	if doc.PageCount() != 1 {
		t.Errorf("PageCount = %d, want 1", doc.PageCount())
	}
}

func TestRebuildRefusesGarbage(t *testing.T) {
	// No object markers means nothing to rebuild from, and the caller should
	// see the original failure rather than a repair-specific one.
	for _, in := range [][]byte{
		[]byte(""),
		[]byte("not a pdf at all"),
		[]byte("%PDF-1.4\ntrailer\n<< >>\n"),
	} {
		if _, err := rebuildXref(bytes.NewReader(in), int64(len(in))); err == nil {
			t.Errorf("rebuildXref accepted %q", in)
		}
	}
}

func TestRebuildRefusesOversizeInput(t *testing.T) {
	// The rebuild reads the whole file, which the streaming path avoids, so
	// it declines rather than blow spec section 9's budget.
	if _, err := rebuildXref(bytes.NewReader(nil), maxRebuildBytes+1); err == nil {
		t.Error("rebuildXref accepted a file above the size cap")
	}
}
