package pdf

import (
	"math"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const eps = 1e-9

// approx compares floats at the tolerance page geometry needs. It is not
// named close so it does not shadow the builtin.
func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func rect(llx, lly, urx, ury float64) *types.Rectangle {
	return types.NewRectangle(llx, lly, urx, ury)
}

func TestMatrixIdentity(t *testing.T) {
	x, y := Identity.Apply(3, 4)
	if !approx(x, 3) || !approx(y, 4) {
		t.Errorf("Identity.Apply(3,4) = (%v,%v), want (3,4)", x, y)
	}
}

func TestMatrixMulOrder(t *testing.T) {
	// PDF composes as: apply the receiver first, then the argument. A
	// translate-then-scale must scale the translation.
	m := Translate(10, 0).Mul(Scale(2, 2))
	x, y := m.Apply(0, 0)
	if !approx(x, 20) || !approx(y, 0) {
		t.Errorf("translate then scale gave (%v,%v), want (20,0)", x, y)
	}

	// The reverse order must not scale the translation.
	m = Scale(2, 2).Mul(Translate(10, 0))
	x, y = m.Apply(0, 0)
	if !approx(x, 10) || !approx(y, 0) {
		t.Errorf("scale then translate gave (%v,%v), want (10,0)", x, y)
	}
}

func TestMatrixMulAssociative(t *testing.T) {
	a := Matrix{A: 2, B: 1, C: -1, D: 3, E: 5, F: 7}
	b := Matrix{A: 0, B: -1, C: 1, D: 0, E: 2, F: -3}
	c := Matrix{A: 1, B: 0.5, C: 0, D: 1, E: -1, F: 4}

	left := a.Mul(b).Mul(c)
	right := a.Mul(b.Mul(c))

	for _, pt := range [][2]float64{{0, 0}, {1, 0}, {0, 1}, {3, -4}} {
		lx, ly := left.Apply(pt[0], pt[1])
		rx, ry := right.Apply(pt[0], pt[1])
		if math.Abs(lx-rx) > eps || math.Abs(ly-ry) > eps {
			t.Errorf("association differs at %v: (%v,%v) vs (%v,%v)", pt, lx, ly, rx, ry)
		}
	}
}

func TestMatrixRotation(t *testing.T) {
	cases := []struct {
		m    Matrix
		want float64
	}{
		{Identity, 0},
		{Matrix{A: 0, B: 1, C: -1, D: 0}, 90},
		{Matrix{A: -1, B: 0, C: 0, D: -1}, 180},
		{Matrix{A: 0, B: -1, C: 1, D: 0}, -90},
		// A y-flip, which is what the page base CTM applies, must still read
		// as zero rotation so upright text is not classified as rotated.
		{Matrix{A: 1, B: 0, C: 0, D: -1}, 0},
	}
	for _, c := range cases {
		if got := c.m.Rotation(); !approx(got, c.want) {
			t.Errorf("Rotation of %+v = %v, want %v", c.m, got, c.want)
		}
	}
}

func TestMatrixScaleXY(t *testing.T) {
	sx, sy := Scale(3, 4).ScaleXY()
	if !approx(sx, 3) || !approx(sy, 4) {
		t.Errorf("ScaleXY = (%v,%v), want (3,4)", sx, sy)
	}

	// Scale survives rotation.
	rot90 := Matrix{A: 0, B: 2, C: -2, D: 0}
	sx, sy = rot90.ScaleXY()
	if !approx(sx, 2) || !approx(sy, 2) {
		t.Errorf("rotated ScaleXY = (%v,%v), want (2,2)", sx, sy)
	}
}

func TestRectUnion(t *testing.T) {
	// A zero-value receiver must fold cleanly, since callers accumulate from
	// Rect{}.
	var acc Rect
	acc = acc.Union(Rect{MinX: 1, MinY: 2, MaxX: 3, MaxY: 4})
	if acc != (Rect{MinX: 1, MinY: 2, MaxX: 3, MaxY: 4}) {
		t.Errorf("union with zero receiver = %+v", acc)
	}

	acc = acc.Union(Rect{MinX: -1, MinY: 0, MaxX: 2, MaxY: 10})
	want := Rect{MinX: -1, MinY: 0, MaxX: 3, MaxY: 10}
	if acc != want {
		t.Errorf("union = %+v, want %+v", acc, want)
	}

	// Unioning with a zero value must not drag the box to the origin.
	acc = acc.Union(Rect{})
	if acc != want {
		t.Errorf("union with zero value changed the box to %+v", acc)
	}
}

func TestBaseCTMOrientation(t *testing.T) {
	// Page space runs y-down from the top-left of the box. A point at the
	// top of a 792pt-tall page in user space must land near y=0.
	box := rect(0, 0, 612, 792)

	m := baseCTM(box, 0)
	x, y := m.Apply(0, 792)
	if !approx(x, 0) || !approx(y, 0) {
		t.Errorf("rotate 0: user top-left maps to (%v,%v), want (0,0)", x, y)
	}
	x, y = m.Apply(612, 0)
	if !approx(x, 612) || !approx(y, 792) {
		t.Errorf("rotate 0: user bottom-right maps to (%v,%v), want (612,792)", x, y)
	}
	if !approx(m.Rotation(), 0) {
		t.Errorf("rotate 0 base CTM reports rotation %v, want 0", m.Rotation())
	}
}

func TestBaseCTMRotations(t *testing.T) {
	box := rect(0, 0, 612, 792)

	// Every rotation must map the four corners of user space onto the four
	// corners of the displayed page, with no point falling outside.
	for _, rot := range []int{0, 90, 180, 270} {
		m := baseCTM(box, rot)
		w, h := 612.0, 792.0
		if rot == 90 || rot == 270 {
			w, h = h, w
		}
		corners := [][2]float64{{0, 0}, {612, 0}, {0, 792}, {612, 792}}
		for _, c := range corners {
			x, y := m.Apply(c[0], c[1])
			if x < -eps || x > w+eps || y < -eps || y > h+eps {
				t.Errorf("rotate %d: user %v maps to (%v,%v), outside the %vx%v page",
					rot, c, x, y, w, h)
			}
		}
	}
}

func TestBaseCTMOffsetBox(t *testing.T) {
	// A crop box not anchored at the origin must be translated so page space
	// still starts at (0,0).
	box := rect(50, 100, 662, 892)
	m := baseCTM(box, 0)
	x, y := m.Apply(50, 892)
	if !approx(x, 0) || !approx(y, 0) {
		t.Errorf("offset box top-left maps to (%v,%v), want (0,0)", x, y)
	}
}
