package pdf

import "math"

// Matrix is a PDF affine transform, stored as the six variable elements of
//
//	| a b 0 |
//	| c d 0 |
//	| e f 1 |
type Matrix struct {
	A, B, C, D, E, F float64
}

// Identity is the no-op transform.
var Identity = Matrix{A: 1, D: 1}

// Mul returns m × n, applying m first and then n. PDF composes transforms in
// this order: a text rendering matrix is Tparams × Tm × CTM.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.B*n.C,
		B: m.A*n.B + m.B*n.D,
		C: m.C*n.A + m.D*n.C,
		D: m.C*n.B + m.D*n.D,
		E: m.E*n.A + m.F*n.C + n.E,
		F: m.E*n.B + m.F*n.D + n.F,
	}
}

// Apply transforms the point (x, y).
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// Translation returns the transformed origin, which for a text rendering
// matrix is the glyph's baseline origin in page space.
func (m Matrix) Translation() (float64, float64) { return m.E, m.F }

// Translate returns a translation matrix.
func Translate(tx, ty float64) Matrix { return Matrix{A: 1, D: 1, E: tx, F: ty} }

// Scale returns a scaling matrix.
func Scale(sx, sy float64) Matrix { return Matrix{A: sx, D: sy} }

// ScaleXY decomposes the matrix into its horizontal and vertical scale
// factors, ignoring rotation and skew.
func (m Matrix) ScaleXY() (float64, float64) {
	return math.Hypot(m.A, m.B), math.Hypot(m.C, m.D)
}

// Rotation returns the matrix rotation in degrees, in (-180, 180].
func (m Matrix) Rotation() float64 {
	if m.A == 0 && m.B == 0 {
		return 0
	}
	return math.Atan2(m.B, m.A) * 180 / math.Pi
}

// Rect is an axis-aligned rectangle in page space.
type Rect struct {
	MinX, MinY, MaxX, MaxY float64
}

// Intersects reports whether two rectangles share any area. Touching edges
// do not count, so a rectangle abutting another is not inside it.
func (r Rect) Intersects(o Rect) bool {
	return r.MinX < o.MaxX && o.MinX < r.MaxX &&
		r.MinY < o.MaxY && o.MinY < r.MaxY
}

// Width returns the horizontal extent.
func (r Rect) Width() float64 { return r.MaxX - r.MinX }

// Height returns the vertical extent.
func (r Rect) Height() float64 { return r.MaxY - r.MinY }

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.Width() <= 0 || r.Height() <= 0 }

// Union returns the smallest rectangle containing both operands. A zero-value
// receiver returns s unchanged, so Union folds over a slice from Rect{}.
func (r Rect) Union(s Rect) Rect {
	if r == (Rect{}) {
		return s
	}
	if s == (Rect{}) {
		return r
	}
	return Rect{
		MinX: math.Min(r.MinX, s.MinX),
		MinY: math.Min(r.MinY, s.MinY),
		MaxX: math.Max(r.MaxX, s.MaxX),
		MaxY: math.Max(r.MaxY, s.MaxY),
	}
}
