// Package theme reads the colour scheme of an Office theme, which Word,
// Excel and PowerPoint files all refer to: a file names a colour as
// "accent1" or as theme colour 4, and the theme part says what that is.
package theme

import (
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Color is a colour as red, green and blue.
type Color struct{ R, G, B uint8 }

// White and Black are the page and the ink where nothing else is said.
var (
	White = Color{255, 255, 255}
	Black = Color{}
)

// Scheme is the colour scheme of a theme: twelve colours by name (dk1, lt1,
// dk2, lt2, accent1 to accent6, hlink, folHlink). A nil Scheme has none.
type Scheme struct {
	colors map[string]Color
}

// Read reads the colour scheme from a theme part.
func Read(s *xmlscan.Scanner) *Scheme {
	t := &Scheme{colors: map[string]Color{}}
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Draw, "clrScheme") {
			continue
		}
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
		break
	}
	return t
}

// Named returns the colour with the given scheme name.
func (t *Scheme) Named(name string) (Color, bool) {
	if t == nil {
		return Color{}, false
	}
	c, ok := t.colors[name]
	return c, ok
}

// Colors returns the scheme's colours by name.
func (t *Scheme) Colors() map[string]Color {
	if t == nil {
		return nil
	}
	return t.colors
}

// sheetOrder is the order in which a spreadsheet numbers the colours of
// the theme. The first four are not in the order the theme lists them:
// light comes before dark.
var sheetOrder = [...]string{"lt1", "dk1", "lt2", "dk2", "accent1", "accent2", "accent3", "accent4", "accent5", "accent6", "hlink", "folHlink"}

// Indexed returns the colour a spreadsheet means by a theme colour number.
func (t *Scheme) Indexed(i int) (Color, bool) {
	if i < 0 || i >= len(sheetOrder) {
		return Color{}, false
	}
	return t.Named(sheetOrder[i])
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

// Tint lightens (tint above zero) or darkens (below) a colour the way a
// spreadsheet does: by moving its lightness toward white or black by the
// given share, keeping hue and saturation.
func Tint(c Color, tint float64) Color {
	if tint == 0 || tint != tint {
		return c
	}
	tint = min(max(tint, -1), 1)
	h, l, s := toHLS(c)
	if tint < 0 {
		l *= 1 + tint
	} else {
		l = l*(1-tint) + tint
	}
	return fromHLS(h, l, s)
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
