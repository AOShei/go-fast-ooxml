// Package dml reads DrawingML, the part of Office Open XML that Word,
// Excel and PowerPoint share for what is drawn: colours and fills, where
// a shape lies, and the text inside shapes.
package dml

import (
	"math"

	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A drawing names a theme colour in one of two ways: by what it is for
// (bg1, tx1: the first background and text colours) or by where it is in
// the theme (lt1, dk1). The first kind goes through a colour map, which
// each slide master has and a slide or layout may replace, so that the
// same drawing is dark on light under one map and light on dark under
// another.
const (
	slotBg1 = iota
	slotTx1
	slotBg2
	slotTx2
	slotAccent1
	slotAccent2
	slotAccent3
	slotAccent4
	slotAccent5
	slotAccent6
	slotHlink
	slotFolHlink
	slotDk1
	slotLt1
	slotDk2
	slotLt2
	slotCount

	mapped = slotDk1 // the slots before this one go through the map
)

var slotNames = [slotCount]string{"bg1", "tx1", "bg2", "tx2", "accent1", "accent2", "accent3", "accent4", "accent5", "accent6", "hlink", "folHlink", "dk1", "lt1", "dk2", "lt2"}

func slotOf(name []byte) (uint8, bool) {
	for i, n := range slotNames {
		if string(name) == n {
			return uint8(i), true
		}
	}
	return 0, false
}

// ColorMap says which colour of the theme each mapped name stands for.
type ColorMap [mapped]uint8

// DefaultMap is the map of a drawing that has none: dark text on a light
// background.
var DefaultMap = ColorMap{slotLt1, slotDk1, slotLt2, slotDk2, slotAccent1, slotAccent2, slotAccent3, slotAccent4, slotAccent5, slotAccent6, slotHlink, slotFolHlink}

// ReadColorMap reads a colour map from the attributes of the current
// element (p:clrMap, a:overrideClrMapping). What the element does not
// say stays as in DefaultMap.
func ReadColorMap(s *xmlscan.Scanner) ColorMap {
	m := DefaultMap
	for i := 0; i < mapped; i++ {
		v, ok := s.Attr(xmlscan.NoSpace, slotNames[i])
		if !ok {
			continue
		}
		// A map names colours of the theme, never the names it maps.
		if to, ok := slotOf(v); ok && to >= slotAccent1 {
			m[i] = to
		}
	}
	return m
}

// Palette is what turns a colour as a drawing names it into red, green
// and blue: the theme and the colour map in force.
type Palette struct {
	Theme *theme.Scheme
	Map   ColorMap
}

// Kinds of Color.
const (
	colorNone        uint8 = iota
	colorRGB               // red, green and blue as written
	colorScheme            // a colour of the theme
	colorPlaceholder       // phClr: the colour of whatever uses the style
	colorUnknown           // written in a way that is not worked out
)

// Adjustments to a colour.
const (
	modAlpha uint8 = iota + 1
	modAlphaMod
	modLumMod
	modLumOff
	modTint
	modShade
	modSatMod
)

type mod struct {
	op  uint8
	val int32 // thousandths of a percent
}

// Color is a colour as a drawing writes it, kept unresolved: what a theme
// colour is depends on the palette in force where it is used, and a
// style defined on a slide master is used under the palette of each
// slide. The zero Color is no colour.
type Color struct {
	kind uint8
	slot uint8
	n    uint8
	rgb  theme.Color
	mods [4]mod
}

// IsSet reports whether c is a colour at all.
func (c Color) IsSet() bool { return c.kind != colorNone }

// Scheme returns the colour a drawing names by the given scheme name
// ("tx1", "accent2"), and whether there is such a name.
func Scheme(name string) (Color, bool) {
	slot, ok := slotOf([]byte(name))
	return Color{kind: colorScheme, slot: slot}, ok
}

// Resolve works the colour out. alpha is its opacity from 0 to 1. ok is
// false if the colour is none, is written in a way this package does not
// work out, or names something the palette does not have. ph is what a
// placeholder colour stands for.
func (p *Palette) Resolve(c Color, ph *theme.Color) (rgb theme.Color, alpha float64, ok bool) {
	switch c.kind {
	case colorRGB:
		rgb = c.rgb
	case colorScheme:
		slot := c.slot
		if slot < mapped {
			slot = p.Map[slot]
		}
		if rgb, ok = p.Theme.Named(slotNames[slot]); !ok {
			return rgb, 0, false
		}
	case colorPlaceholder:
		if ph == nil {
			return rgb, 0, false
		}
		rgb = *ph
	default:
		return rgb, 0, false
	}
	alpha = 1
	for _, m := range c.mods[:c.n] {
		v := float64(m.val) / 100000
		switch m.op {
		case modAlpha:
			alpha = v
		case modAlphaMod:
			alpha *= v
		case modLumMod:
			h, l, s := theme.HLS(rgb)
			rgb = theme.FromHLS(h, l*v, s)
		case modLumOff:
			h, l, s := theme.HLS(rgb)
			rgb = theme.FromHLS(h, l+v, s)
		case modSatMod:
			h, l, s := theme.HLS(rgb)
			rgb = theme.FromHLS(h, l, s*v)
		case modTint:
			// Toward white, in linear light.
			rgb = mix(rgb, v, 1-v)
		case modShade:
			rgb = mix(rgb, v, 0) // toward black
		}
	}
	return rgb, min(max(alpha, 0), 1), true
}

// mix returns c scaled by keep with add added, channel by channel in
// linear light.
func mix(c theme.Color, keep, add float64) theme.Color {
	keep = min(max(keep, 0), 1)
	one := func(v uint8) uint8 {
		lin := toLinear(float64(v)/255)*keep + add
		return uint8(min(max(fromLinear(lin), 0), 1)*255 + 0.5)
	}
	return theme.Color{R: one(c.R), G: one(c.G), B: one(c.B)}
}

func toLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func fromLinear(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// The few preset colour names that are plain enough to meet in practice.
// Any other preset colour is not worked out.
var presets = map[string]theme.Color{
	"black": {}, "white": {R: 255, G: 255, B: 255}, "red": {R: 255}, "green": {G: 128}, "blue": {B: 255},
	"yellow": {R: 255, G: 255}, "gray": {R: 128, G: 128, B: 128}, "grey": {R: 128, G: 128, B: 128},
	"silver": {R: 192, G: 192, B: 192}, "lime": {G: 255}, "navy": {B: 128}, "orange": {R: 255, G: 165},
	"cyan": {G: 255, B: 255}, "magenta": {R: 255, B: 255}, "purple": {R: 128, B: 128}, "maroon": {R: 128},
}

// ReadColor reads the colour inside the element whose Start token is
// current (a:solidFill, a:fillRef, a:fontRef, a:highlight, a:buClr) and
// consumes the element. An element with no colour in it gives the zero
// Color.
func ReadColor(s *xmlscan.Scanner) Color {
	var c Color
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if c.kind != colorNone {
			s.Skip() // a second colour: the first stands
			continue
		}
		c = ReadColorElement(s)
	}
	return c
}

// ReadColorElement reads the colour element whose Start token is current
// (a:srgbClr, a:schemeClr and the like), with the adjustments inside it,
// and consumes it. Any other element gives a colour that is not worked
// out.
func ReadColorElement(s *xmlscan.Scanner) Color {
	c := Color{kind: colorUnknown}
	if s.Space() != ns.Draw {
		s.Skip()
		return c
	}
	switch string(s.Local()) {
	case "srgbClr":
		v, _ := s.Attr(xmlscan.NoSpace, "val")
		if rgb, ok := theme.Hex(v); ok {
			c = Color{kind: colorRGB, rgb: rgb}
		}
	case "sysClr":
		// A colour of the operating system; the file records what it
		// was when the file was saved.
		v, _ := s.Attr(xmlscan.NoSpace, "lastClr")
		if rgb, ok := theme.Hex(v); ok {
			c = Color{kind: colorRGB, rgb: rgb}
		}
	case "schemeClr":
		v, _ := s.Attr(xmlscan.NoSpace, "val")
		if slot, ok := slotOf(v); ok {
			c = Color{kind: colorScheme, slot: slot}
		} else if string(v) == "phClr" {
			c.kind = colorPlaceholder
		}
	case "prstClr":
		v, _ := s.Attr(xmlscan.NoSpace, "val")
		if rgb, ok := presets[string(v)]; ok {
			c = Color{kind: colorRGB, rgb: rgb}
		}
	case "scrgbClr":
		r, ok1 := percentAttr(s, "r")
		g, ok2 := percentAttr(s, "g")
		b, ok3 := percentAttr(s, "b")
		if ok1 && ok2 && ok3 {
			// Shares of full intensity, in linear light.
			one := func(v int64) uint8 {
				return uint8(min(max(fromLinear(float64(v)/100000), 0), 1)*255 + 0.5)
			}
			c = Color{kind: colorRGB, rgb: theme.Color{R: one(r), G: one(g), B: one(b)}}
		}
	}
	// The adjustments, in the order they are applied.
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if c.kind == colorUnknown || s.Space() != ns.Draw {
			s.Skip()
			continue
		}
		var op uint8
		switch string(s.Local()) {
		case "alpha":
			op = modAlpha
		case "alphaMod":
			op = modAlphaMod
		case "lumMod":
			op = modLumMod
		case "lumOff":
			op = modLumOff
		case "tint":
			op = modTint
		case "shade":
			op = modShade
		case "satMod":
			op = modSatMod
		}
		v, ok := percentAttr(s, "val")
		if op == 0 || !ok || int(c.n) == len(c.mods) || v < -10000000 || v > 10000000 {
			c.kind = colorUnknown
		} else {
			c.mods[c.n] = mod{op: op, val: int32(v)}
			c.n++
		}
		s.Skip()
	}
	return c
}

// Paint is the kind of a fill.
type Paint uint8

const (
	PaintUnset Paint = iota // nothing is said
	PaintNone               // no fill: what is behind shows through
	PaintSolid              // one colour
	PaintOther              // a gradient, a picture, a pattern, the fill of a group
)

// Fill is how an area or the letters of text are painted.
type Fill struct {
	Paint Paint
	Color Color // for PaintSolid

	// For PaintOther, what is known of it: the colours of a gradient's
	// stops, or a picture (the relationship of its image, which the
	// reader turns into the part's name), with the share of the picture
	// cut off each side (left, top, right, bottom) and whether it is
	// tiled rather than stretched.
	Stops []Color
	Blip  string
	Crop  [4]float64
	Tile  bool
}

// ReadFill reads the current element if it is one of the fill elements
// (a:solidFill, a:noFill, a:gradFill, a:blipFill, a:pattFill, a:grpFill)
// and consumes it. ok is false, and nothing is consumed, for any other
// element.
func ReadFill(s *xmlscan.Scanner) (f Fill, ok bool) {
	if s.Space() != ns.Draw {
		return f, false
	}
	switch string(s.Local()) {
	case "solidFill":
		f = Fill{Paint: PaintSolid, Color: ReadColor(s)}
		if !f.Color.IsSet() {
			f.Paint = PaintOther // a solid fill of no stated colour
		}
	case "noFill":
		f.Paint = PaintNone
		s.Skip()
	case "gradFill":
		f.Paint = PaintOther
		f.Stops = readStops(s)
	case "blipFill":
		f.Paint = PaintOther
		f.Blip, f.Crop, f.Tile = ReadBlipFill(s)
	case "pattFill", "grpFill":
		f.Paint = PaintOther
		s.Skip()
	default:
		return f, false
	}
	return f, true
}

// maxStops bounds the stops of a gradient that are kept.
const maxStops = 16

// readStops reads the colours of a:gradFill's stops.
func readStops(s *xmlscan.Scanner) []Color {
	var stops []Color
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() == xmlscan.Start && s.Is(ns.Draw, "gs") {
			if c := ReadColor(s); len(stops) < maxStops {
				stops = append(stops, c)
			}
		}
	}
	return stops
}

// ReadBlipFill reads a blip fill (a:blipFill, p:blipFill) whose start tag
// is current: the relationship of its picture, what is cut off it, and
// whether it is tiled.
func ReadBlipFill(s *xmlscan.Scanner) (blip string, crop [4]float64, tile bool) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Draw, "blip"):
			if id, ok := s.Attr(ns.Rel, "embed"); ok && blip == "" {
				blip = string(id)
			}
		case s.Is(ns.Draw, "srcRect"):
			for i, side := range []string{"l", "t", "r", "b"} {
				if v, ok := percentAttr(s, side); ok {
					crop[i] = float64(v) / 100000
				}
			}
		case s.Is(ns.Draw, "tile"):
			tile = true
		}
	}
	return blip, crop, tile
}

// Colors resolves the stops of a gradient. ok is false unless every stop
// is a known colour at full opacity.
func (p *Palette) Colors(stops []Color) ([]theme.Color, bool) {
	if len(stops) == 0 {
		return nil, false
	}
	out := make([]theme.Color, len(stops))
	for i, c := range stops {
		rgb, alpha, ok := p.Resolve(c, nil)
		if !ok || alpha < 1 {
			return nil, false
		}
		out[i] = rgb
	}
	return out, true
}

// StyleFill returns the fill a shape gets from a reference to one of the
// theme's fill styles (a:fillRef, p:bgRef): index is the reference's
// number and c its colour.
func (p *Palette) StyleFill(index int, c Color) Fill {
	plain, none := p.Theme.PlainFill(index)
	switch {
	case none:
		return Fill{Paint: PaintNone}
	case plain && c.IsSet():
		return Fill{Paint: PaintSolid, Color: c}
	}
	return Fill{Paint: PaintOther}
}

// Opaque works out the colour of a fill that hides what is behind it:
// one plain colour at full opacity. ok is false for any other fill.
func (p *Palette) Opaque(f Fill, ph *theme.Color) (rgb theme.Color, ok bool) {
	if f.Paint != PaintSolid {
		return rgb, false
	}
	rgb, alpha, ok := p.Resolve(f.Color, ph)
	return rgb, ok && alpha >= 1
}

// intAttr reads an attribute that is a whole number.
func intAttr(s *xmlscan.Scanner, name string) (int64, bool) {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok {
		return 0, false
	}
	return atoi(v)
}

// percentAttr reads an attribute that is a share, in thousandths of a
// percent. Transitional files write "50000"; Strict files write "50%".
func percentAttr(s *xmlscan.Scanner, name string) (int64, bool) {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok {
		return 0, false
	}
	if n := len(v); n > 1 && v[n-1] == '%' {
		whole, frac := v[:n-1], []byte(nil)
		for i, c := range whole {
			if c == '.' {
				whole, frac = whole[:i], whole[i+1:]
				break
			}
		}
		w, ok := atoi(whole)
		if !ok {
			return 0, false
		}
		w *= 1000
		// Up to three decimals count.
		scale := int64(100)
		for _, c := range frac {
			if c < '0' || c > '9' {
				return 0, false
			}
			if w < 0 {
				w -= int64(c-'0') * scale
			} else {
				w += int64(c-'0') * scale
			}
			scale /= 10
		}
		return w, true
	}
	return atoi(v)
}

// atoi reads a whole number, with an optional sign. Numbers too large to
// matter are refused, so that nothing computed from one can overflow.
func atoi(v []byte) (int64, bool) {
	neg := false
	if len(v) > 0 && (v[0] == '-' || v[0] == '+') {
		neg = v[0] == '-'
		v = v[1:]
	}
	if len(v) == 0 || len(v) > 15 {
		return 0, false
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}
