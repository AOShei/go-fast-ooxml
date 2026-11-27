package dml

import (
	"math"

	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
)

// EMU is the number of English Metric Units, in which drawings are
// measured, to a point.
const EMU = 12700.0

// Matrix is an affine map of the plane: a point (x, y) goes to
// (A*x + C*y + E, B*x + D*y + F). It is how the place of a shape inside
// groups that are themselves moved, scaled, turned and mirrored is
// worked out.
type Matrix struct{ A, B, C, D, E, F float64 }

// Identity maps every point to itself.
var Identity = Matrix{A: 1, D: 1}

// Then returns the map that applies m first and outer after it.
func (m Matrix) Then(outer Matrix) Matrix {
	return Matrix{
		A: outer.A*m.A + outer.C*m.B,
		B: outer.B*m.A + outer.D*m.B,
		C: outer.A*m.C + outer.C*m.D,
		D: outer.B*m.C + outer.D*m.D,
		E: outer.A*m.E + outer.C*m.F + outer.E,
		F: outer.B*m.E + outer.D*m.F + outer.F,
	}
}

// Apply returns where m takes the point (x, y).
func (m Matrix) Apply(x, y float64) (float64, float64) { return m.apply(x, y) }

func (m Matrix) apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// Xfrm is where a shape or a group lies (a:xfrm), in points: its corner
// and size in the space of what contains it, how far it is turned
// clockwise about its centre, and whether it is mirrored. A group also
// says which rectangle of its children's space fills its box.
type Xfrm struct {
	Set          bool
	X, Y, W, H   float64
	Rot          float64 // degrees
	FlipH, FlipV bool

	Child              bool
	ChX, ChY, ChW, ChH float64
}

// ReadXfrm reads the element whose Start token is current (a:xfrm,
// p:xfrm) and consumes it.
func ReadXfrm(s *xmlscan.Scanner) Xfrm {
	var x Xfrm
	if n, ok := intAttr(s, "rot"); ok {
		x.Rot = float64(n) / 60000
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "flipH"); ok {
		x.FlipH = string(v) == "1" || string(v) == "true"
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "flipV"); ok {
		x.FlipV = string(v) == "1" || string(v) == "true"
	}
	pair := func(a, b string) (float64, float64, bool) {
		p, ok1 := intAttr(s, a)
		q, ok2 := intAttr(s, b)
		return float64(p) / EMU, float64(q) / EMU, ok1 && ok2
	}
	var off, ext bool
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Space() == ns.Draw {
			switch string(s.Local()) {
			case "off":
				x.X, x.Y, off = pair("x", "y")
			case "ext":
				x.W, x.H, ext = pair("cx", "cy")
			case "chOff":
				x.ChX, x.ChY, _ = pair("x", "y")
			case "chExt":
				x.ChW, x.ChH, x.Child = pair("cx", "cy")
			}
		}
		s.Skip()
	}
	x.Set = off && ext && x.W >= 0 && x.H >= 0
	return x
}

// Local returns the map from the shape's own box, whose corner is the
// origin, to the space of what contains the shape.
func (x Xfrm) Local() Matrix {
	m := Identity
	if x.FlipH {
		m.A, m.E = -1, x.W
	}
	if x.FlipV {
		m.D, m.F = -1, x.H
	}
	if x.Rot != 0 {
		sin, cos := math.Sincos(x.Rot * math.Pi / 180)
		cx, cy := x.W/2, x.H/2
		m = m.Then(Matrix{A: cos, B: sin, C: -sin, D: cos, E: cx - cx*cos + cy*sin, F: cy - cx*sin - cy*cos})
	}
	return m.Then(Matrix{A: 1, D: 1, E: x.X, F: x.Y})
}

// Children returns the map from the space of a group's children to the
// space of what contains the group.
func (x Xfrm) Children() Matrix {
	sx, sy := 1.0, 1.0
	chX, chY := x.ChX, x.ChY
	if x.Child {
		if x.ChW > 0 {
			sx = x.W / x.ChW
		}
		if x.ChH > 0 {
			sy = x.H / x.ChH
		}
	} else {
		chX, chY = x.X, x.Y // a group that does not say has its children where they say
	}
	return Matrix{A: sx, D: sy, E: -chX * sx, F: -chY * sy}.Then(x.Local())
}

// Rect is a rectangle with its sides along the axes.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// maxExtent bounds the coordinates this package will state, in points.
// Anything further out is a file playing with numbers.
const maxExtent = 1e7

// Bounds returns the smallest upright rectangle around a box of the
// given size once m has placed it. upright says the box itself still has
// its sides along the axes, so that the rectangle is the box. ok is
// false if the result is not a sensible place.
func (m Matrix) Bounds(w, h float64) (r Rect, upright, ok bool) {
	r = Rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range [4][2]float64{{0, 0}, {w, 0}, {0, h}, {w, h}} {
		x, y := m.apply(p[0], p[1])
		if !(math.Abs(x) <= maxExtent && math.Abs(y) <= maxExtent) {
			return Rect{}, false, false
		}
		r.X0, r.Y0, r.X1, r.Y1 = min(r.X0, x), min(r.Y0, y), max(r.X1, x), max(r.Y1, y)
	}
	const eps = 1e-9
	scale := math.Abs(m.A) + math.Abs(m.B) + math.Abs(m.C) + math.Abs(m.D)
	upright = math.Abs(m.B)+math.Abs(m.C) <= eps*scale || math.Abs(m.A)+math.Abs(m.D) <= eps*scale
	return r, upright, true
}

// Contains reports whether r covers all of inner.
func (r Rect) Contains(inner Rect) bool {
	return r.X0 <= inner.X0 && r.Y0 <= inner.Y0 && r.X1 >= inner.X1 && r.Y1 >= inner.Y1
}

// Overlaps reports whether r and other share any area.
func (r Rect) Overlaps(other Rect) bool {
	return r.X0 < other.X1 && other.X0 < r.X1 && r.Y0 < other.Y1 && other.Y0 < r.Y1
}
