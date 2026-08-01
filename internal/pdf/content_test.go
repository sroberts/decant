package pdf

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/sroberts/decant/internal/testpdf"
)

// interpretPage builds a one-page PDF around content and returns its glyphs.
func interpretPage(t *testing.T, content string) *PageContent {
	t.Helper()
	data := testpdf.New().AddPage(612, 792, content).Build()

	doc, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	return doc.Glyphs(page)
}

func glyphText(pc *PageContent) string {
	var sb strings.Builder
	for _, g := range pc.Glyphs {
		sb.WriteRune(g.Rune)
	}
	return sb.String()
}

func TestInterpretBasicText(t *testing.T) {
	pc := interpretPage(t, "BT /F1 12 Tf 72 720 Td (Hello) Tj ET")

	if got := glyphText(pc); got != "Hello" {
		t.Errorf("glyph text = %q, want %q", got, "Hello")
	}
	if len(pc.Glyphs) != 5 {
		t.Fatalf("got %d glyphs, want 5", len(pc.Glyphs))
	}

	// The first glyph sits at the text position, converted to page space:
	// x unchanged, y flipped against the 792pt page height.
	g := pc.Glyphs[0]
	if !approx(g.X, 72) {
		t.Errorf("first glyph x = %v, want 72", g.X)
	}
	if !approx(g.Y, 72) {
		t.Errorf("first glyph y = %v, want 72 (792 - 720)", g.Y)
	}
	if !approx(g.Size, 12) {
		t.Errorf("first glyph size = %v, want 12", g.Size)
	}
	// Helvetica 'H' is 722/1000 em.
	if !approx(g.Advance, 722.0/1000*12) {
		t.Errorf("first glyph advance = %v, want %v", g.Advance, 722.0/1000*12)
	}
}

func TestInterpretAdvancesAccumulate(t *testing.T) {
	pc := interpretPage(t, "BT /F1 10 Tf 100 700 Td (AV) Tj ET")
	if len(pc.Glyphs) != 2 {
		t.Fatalf("got %d glyphs, want 2", len(pc.Glyphs))
	}
	// Helvetica 'A' is 667/1000.
	wantX := 100 + 667.0/1000*10
	if !approx(pc.Glyphs[1].X, wantX) {
		t.Errorf("second glyph x = %v, want %v", pc.Glyphs[1].X, wantX)
	}
}

func TestInterpretCharAndWordSpacing(t *testing.T) {
	// Tc adds to every glyph; Tw adds only at single-byte code 32.
	pc := interpretPage(t, "BT /F1 10 Tf 5 Tc 100 700 Td (AB) Tj ET")
	wantX := 100 + 667.0/1000*10 + 5
	if !approx(pc.Glyphs[1].X, wantX) {
		t.Errorf("with Tc 5, second glyph x = %v, want %v", pc.Glyphs[1].X, wantX)
	}

	pc = interpretPage(t, "BT /F1 10 Tf 20 Tw 100 700 Td ( A) Tj ET")
	// Helvetica space is 278/1000, plus the 20pt word space.
	wantX = 100 + 278.0/1000*10 + 20
	if !approx(pc.Glyphs[1].X, wantX) {
		t.Errorf("with Tw 20, second glyph x = %v, want %v", pc.Glyphs[1].X, wantX)
	}
}

func TestInterpretHorizontalScaling(t *testing.T) {
	pc := interpretPage(t, "BT /F1 10 Tf 50 Tz 100 700 Td (AA) Tj ET")
	// Tz 50 halves both the advance and the effective horizontal size.
	wantX := 100 + 667.0/1000*10*0.5
	if !approx(pc.Glyphs[1].X, wantX) {
		t.Errorf("with Tz 50, second glyph x = %v, want %v", pc.Glyphs[1].X, wantX)
	}
}

func TestInterpretTJAdjustments(t *testing.T) {
	// A positive TJ number moves left by that many thousandths of an em.
	pc := interpretPage(t, "BT /F1 10 Tf 100 700 Td [(A) 500 (B)] TJ ET")
	if len(pc.Glyphs) != 2 {
		t.Fatalf("got %d glyphs, want 2", len(pc.Glyphs))
	}
	wantX := 100 + 667.0/1000*10 - 500.0/1000*10
	if !approx(pc.Glyphs[1].X, wantX) {
		t.Errorf("after a TJ adjustment, x = %v, want %v", pc.Glyphs[1].X, wantX)
	}
}

func TestInterpretTextLineOperators(t *testing.T) {
	// T* moves down by the leading; page space runs y-down, so y increases.
	pc := interpretPage(t, "BT /F1 10 Tf 14 TL 72 700 Td (A) Tj T* (B) Tj ET")
	if len(pc.Glyphs) != 2 {
		t.Fatalf("got %d glyphs, want 2", len(pc.Glyphs))
	}
	if !approx(pc.Glyphs[1].Y-pc.Glyphs[0].Y, 14) {
		t.Errorf("T* moved y by %v, want 14", pc.Glyphs[1].Y-pc.Glyphs[0].Y)
	}
	if !approx(pc.Glyphs[1].X, 72) {
		t.Errorf("T* left x at %v, want 72", pc.Glyphs[1].X)
	}
}

func TestInterpretQuoteOperators(t *testing.T) {
	// ' is T* followed by a show.
	pc := interpretPage(t, "BT /F1 10 Tf 14 TL 72 700 Td (A) Tj (B) ' ET")
	if got := glyphText(pc); got != "AB" {
		t.Errorf("glyph text = %q, want AB", got)
	}
	if !approx(pc.Glyphs[1].Y-pc.Glyphs[0].Y, 14) {
		t.Errorf("' did not advance a line")
	}

	// " sets word and character spacing, then behaves like '.
	pc = interpretPage(t, `BT /F1 10 Tf 14 TL 72 700 Td (A) Tj 20 5 (B) " ET`)
	if got := glyphText(pc); got != "AB" {
		t.Errorf(`after ", glyph text = %q, want AB`, got)
	}
}

func TestInterpretGraphicsStateStack(t *testing.T) {
	// The q/Q pair must restore the CTM, so both glyphs land at the same
	// place despite the cm inside the pair.
	pc := interpretPage(t,
		"BT /F1 10 Tf 72 700 Td (A) Tj ET "+
			"q 1 0 0 1 100 100 cm BT /F1 10 Tf 72 700 Td (B) Tj ET Q "+
			"BT /F1 10 Tf 72 700 Td (C) Tj ET")

	if len(pc.Glyphs) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(pc.Glyphs))
	}
	a, b, c := pc.Glyphs[0], pc.Glyphs[1], pc.Glyphs[2]
	if !approx(a.X, c.X) || !approx(a.Y, c.Y) {
		t.Errorf("Q did not restore the CTM: A at (%v,%v), C at (%v,%v)", a.X, a.Y, c.X, c.Y)
	}
	if approx(a.X, b.X) && approx(a.Y, b.Y) {
		t.Error("cm inside the q/Q pair had no effect")
	}
	if !approx(b.X, a.X+100) {
		t.Errorf("cm translated x to %v, want %v", b.X, a.X+100)
	}
}

func TestInterpretUnbalancedQIsSafe(t *testing.T) {
	// More Q than q must not underflow the stack.
	pc := interpretPage(t, "Q Q Q BT /F1 10 Tf 72 700 Td (A) Tj ET Q")
	if got := glyphText(pc); got != "A" {
		t.Errorf("glyph text = %q, want A", got)
	}
}

func TestInterpretInvisibleTextIsTagged(t *testing.T) {
	pc := interpretPage(t, "BT /F1 10 Tf 3 Tr 72 700 Td (Hidden) Tj ET")
	if len(pc.Glyphs) == 0 {
		t.Fatal("mode 3 text was discarded during interpretation")
	}
	for _, g := range pc.Glyphs {
		if g.RenderMode != 3 {
			t.Errorf("render mode = %d, want 3", g.RenderMode)
		}
		if g.Visible() {
			t.Error("mode 3 glyph reports itself visible")
		}
	}
}

func TestInterpretIgnoresInlineImage(t *testing.T) {
	// The binary payload must not be tokenized as operators.
	pc := interpretPage(t,
		"BT /F1 10 Tf 72 700 Td (A) Tj ET "+
			"BI /W 2 /H 2 /BPC 8 /CS /G ID \x00\x01\x02\x03 EI "+
			"BT /F1 10 Tf 72 680 Td (B) Tj ET")
	if got := glyphText(pc); got != "AB" {
		t.Errorf("glyph text = %q, want AB", got)
	}
}

func TestInterpretTextBeforeFontIsSafe(t *testing.T) {
	// Showing text with no Tf must not panic and must emit nothing.
	pc := interpretPage(t, "BT 72 700 Td (orphan) Tj ET")
	if len(pc.Glyphs) != 0 {
		t.Errorf("got %d glyphs from text with no font, want 0", len(pc.Glyphs))
	}
}

func TestInterpretZeroFontSizeEmitsNothing(t *testing.T) {
	pc := interpretPage(t, "BT /F1 0 Tf 72 700 Td (invisible) Tj ET")
	if len(pc.Glyphs) != 0 {
		t.Errorf("got %d glyphs at font size 0, want 0", len(pc.Glyphs))
	}
}

func TestInterpretRotatedTextRecordsAngle(t *testing.T) {
	// A Tm rotating 90 degrees counter-clockwise in user space, on an
	// unrotated page, is sideways relative to the page.
	pc := interpretPage(t, "BT /F1 10 Tf 0 1 -1 0 300 300 Tm (Side) Tj ET")
	if len(pc.Glyphs) == 0 {
		t.Fatal("no glyphs")
	}
	for _, g := range pc.Glyphs {
		if math.Abs(math.Abs(g.Rotation)-90) > 1 {
			t.Errorf("rotation = %v, want about +/-90", g.Rotation)
		}
	}
}

func TestInterpretTextRiseIsRecorded(t *testing.T) {
	pc := interpretPage(t, "BT /F1 10 Tf 4 Ts 72 700 Td (sup) Tj ET")
	for _, g := range pc.Glyphs {
		if !approx(g.Rise, 4) {
			t.Errorf("rise = %v, want 4", g.Rise)
		}
	}
	// A positive rise moves up the page, which is a decrease in page-space y.
	flat := interpretPage(t, "BT /F1 10 Tf 72 700 Td (sup) Tj ET")
	if pc.Glyphs[0].Y >= flat.Glyphs[0].Y {
		t.Errorf("rise did not move the glyph up: y=%v vs %v",
			pc.Glyphs[0].Y, flat.Glyphs[0].Y)
	}
}

func TestInterpretMalformedOperandsAreSafe(t *testing.T) {
	// Every one of these is nonsense that a damaged file could contain. None
	// may panic.
	for _, src := range []string{
		"BT /F1 Tf (a) Tj ET",
		"BT /F1 10 Tf Td (a) Tj ET",
		"1 2 cm",
		"BT Tm ET",
		"[ ] TJ",
		"[ (a) ] TJ",
		"/Missing Do",
		"gs",
		"BT /F1 10 Tf 99 Tr 72 700 Td (a) Tj ET",
		strings.Repeat("q ", 1000),
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %q: %v", src, r)
				}
			}()
			interpretPage(t, src)
		}()
	}
}

// FuzzInterpret asserts the interpreter never panics on arbitrary content
// streams. Spec section 10 requires this alongside the lexer fuzz target.
func FuzzInterpret(f *testing.F) {
	seeds := []string{
		"BT /F1 12 Tf 72 720 Td (hello) Tj ET",
		"BT /F1 12 Tf 14 TL 72 720 Td (a) Tj T* (b) ' ET",
		"q 2 0 0 2 0 0 cm BT /F1 6 Tf (scaled) Tj ET Q",
		"BT /F1 10 Tf [ (kern) -250 (ed) ] TJ ET",
		"BI /W 1 /H 1 ID \x00 EI",
		"BT /F1 10 Tf 0 1 -1 0 0 0 Tm (rot) Tj ET",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	// Build the surrounding document once; only the content stream varies.
	f.Fuzz(func(t *testing.T, content []byte) {
		// A content stream carrying a raw "endstream" would break the
		// fixture's own syntax rather than the interpreter.
		if bytes.Contains(content, []byte("endstream")) {
			t.Skip()
		}
		data := testpdf.New().AddPage(612, 792, string(content)).Build()

		doc, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		page, err := doc.Page(0)
		if err != nil {
			return
		}
		pc := doc.Glyphs(page)
		for _, g := range pc.Glyphs {
			if math.IsNaN(g.X) || math.IsNaN(g.Y) {
				t.Fatalf("glyph position is NaN: %+v", g)
			}
			if math.IsInf(g.X, 0) || math.IsInf(g.Y, 0) {
				t.Fatalf("glyph position is infinite: %+v", g)
			}
		}
	})
}
