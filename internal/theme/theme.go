// Package theme reads the colour scheme of an Office theme, which Word,
// Excel and PowerPoint files all refer to: a file names a colour as
// "accent1" or as theme colour 4, and the theme part says what that is.
package theme

import (
	"math"

	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
)

// Color is a colour as red, green and blue.
type Color struct{ R, G, B uint8 }

// Scheme is the colour scheme of a theme: twelve colours by name (dk1, lt1,
// dk2, lt2, accent1 to accent6, hlink, folHlink). A nil Scheme has
// nothing.
type Scheme struct {
	colors map[string]Color
}

// Read reads the colour scheme from a theme part.
func Read(s *xmlscan.Scanner) *Scheme {
	t := &Scheme{colors: map[string]Color{}}
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Space() != ns.Draw {
			continue
		}
		switch string(s.Local()) {
		case "clrScheme":
			t.readColors(s)
		case "fmtScheme", "objectDefaults", "extraClrSchemeLst":
			return t
		}
	}
	return t
}

func (t *Scheme) readColors(s *xmlscan.Scanner) {
	depth := s.Depth()
	name := ""
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Space() != ns.Draw {
			continue
		}
		switch {
		case s.Depth() == depth+1:
			name = string(s.Local())
		case s.Depth() == depth+2 && s.Is(ns.Draw, "srgbClr"):
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			if c, ok := Hex(v); ok && len(name) <= 16 {
				t.colors[name] = c
			}
		case s.Depth() == depth+2 && s.Is(ns.Draw, "sysClr"):
			// A colour of the operating system; the file records what
			// it was when the file was saved.
			v, _ := s.Attr(xmlscan.NoSpace, "lastClr")
			if c, ok := Hex(v); ok && len(name) <= 16 {
				t.colors[name] = c
			}
		}
	}
}

// Named returns the colour with the given scheme name.
func (t *Scheme) Named(name string) (Color, bool) {
	if t == nil {
		return Color{}, false
	}
	c, ok := t.colors[name]
	return c, ok
}

// Hex reads a colour written as six hexadecimal digits.
func Hex(v []byte) (Color, bool) {
	if len(v) != 6 {
		return Color{}, false
	}
	var n [3]uint8
	for i, c := range v {
		var d uint8
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return Color{}, false
		}
		n[i/2] = n[i/2]<<4 | d
	}
	return Color{n[0], n[1], n[2]}, true
}

// HLS returns a colour's hue, lightness and saturation, each from 0 to 1.
func HLS(c Color) (h, l, s float64) { return toHLS(c) }

// FromHLS returns the colour with the given hue, lightness and
// saturation.
func FromHLS(h, l, s float64) Color {
	return fromHLS(h-math.Floor(h), min(max(l, 0), 1), min(max(s, 0), 1))
}

func toHLS(c Color) (h, l, s float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	if hi == lo {
		return 0, l, 0
	}
	d := hi - lo
	if l > 0.5 {
		s = d / (2 - hi - lo)
	} else {
		s = d / (hi + lo)
	}
	switch hi {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, l, s
}

func fromHLS(h, l, s float64) Color {
	if s == 0 {
		v := channel(l)
		return Color{v, v, v}
	}
	q := l + s - l*s
	if l < 0.5 {
		q = l * (1 + s)
	}
	p := 2*l - q
	return Color{channel(hue(p, q, h+1.0/3)), channel(hue(p, q, h)), channel(hue(p, q, h-1.0/3))}
}

func hue(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 0.5:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	}
	return p
}

func channel(v float64) uint8 {
	return uint8(min(max(v, 0), 1)*255 + 0.5)
}
