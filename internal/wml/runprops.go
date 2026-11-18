package wml

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Whether a reader can see a run of text depends on its character
// formatting: hidden, too small, the colour of what is behind it. Word
// stores that formatting in layers. A run's own properties come first,
// then its character style, then its paragraph's style, then the
// document's defaults; each layer supplies what the ones before left
// unset. This file reads the layers and puts them together.
//
// Where two layers both set a property, the nearer one wins, with one
// exception that Word makes for some on/off properties, hidden among them:
// set by the character style and by the paragraph's style both, they
// cancel, so that hidden text in a hidden paragraph is shown. That is
// applied for hidden text. It is not applied to bold and the like, which
// are reported from the run and its character style alone.

// color is a colour that may be unset, or "automatic", which Word draws
// dark on a light background and light on a dark one, so that it is never
// invisible.
type color struct {
	set, auto bool
	unknown   bool // there is a colour, and it could not be worked out
	r, g, b   uint8
}

var white = color{set: true, r: 255, g: 255, b: 255}

// On/off run properties.
const (
	propVanish uint8 = 1 << iota
	propBold
	propItalic
	propUnderline
	propStrike
	propTransparent
	propSqueezed // fitted into a width too small to read
)

// Vertical alignment of a run.
const (
	vertUnset int8 = iota
	vertBaseline
	vertSuper
	vertSub
)

// runProps is one layer of character formatting.
type runProps struct {
	has       uint8 // which on/off properties this layer sets
	on        uint8 // and to what
	size      int16 // in half-points; 0 if not set
	scale     int16 // width of the characters, in percent; 0 if not set
	vert      int8
	color     color
	shade     color // shading behind the run
	highlight color
	style     string // the character style the run names
}

// under fills what p leaves unset from parent.
func (p *runProps) under(parent *runProps) {
	inherit := parent.has &^ p.has
	p.has |= inherit
	p.on |= parent.on & inherit
	if p.size == 0 {
		p.size = parent.size
	}
	if p.scale == 0 {
		p.scale = parent.scale
	}
	if p.vert == vertUnset {
		p.vert = parent.vert
	}
	if !p.color.set {
		p.color = parent.color
	}
	if !p.shade.set {
		p.shade = parent.shade
	}
	if !p.highlight.set {
		p.highlight = parent.highlight
	}
}

func (p *runProps) is(prop uint8) bool { return p.on&prop != 0 }

func (p *runProps) setFlag(prop uint8, on bool) {
	p.has |= prop
	if on {
		p.on |= prop
	} else {
		p.on &^= prop
	}
}

// empty reports whether the layer sets nothing.
func (p *runProps) empty() bool {
	return p.has == 0 && p.size == 0 && p.scale == 0 && p.vert == vertUnset && !p.color.set && !p.shade.set && !p.highlight.set && p.style == ""
}

// onOff reads the value of an on/off element: present without a value, or
// with "1", "true" or "on", is on.
func onOff(s *xmlscan.Scanner) bool {
	v, has := s.Attr(ns.Word, "val")
	return !has || isOn(v)
}

// readRunProps reads w:rPr into a layer. theme resolves theme colour
// names and may be nil.
func readRunProps(s *xmlscan.Scanner, rp *runProps, theme map[string]color) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
			continue
		}
		if s.Space() == ns.Word14 && string(s.Local()) == "textFill" {
			// Word 2010 text effects: a fill with full transparency, or
			// no fill at all, leaves nothing to see.
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start {
					continue
				}
				// The fill is the colour the text is drawn in, whatever
				// w:color says.
				switch string(s.Local()) {
				case "noFill":
					rp.setFlag(propTransparent, true)
				case "alpha":
					if v, ok := s.Attr(ns.Word14, "val"); ok {
						if n, ok := atoi(v); ok && n >= 100000 {
							rp.setFlag(propTransparent, true)
						}
					}
				case "srgbClr":
					rp.color = unknown
					if v, ok := s.Attr(ns.Word14, "val"); ok {
						if c, ok := hexColor(v); ok {
							rp.color = c
						}
					}
				case "schemeClr":
					rp.color = unknown
					if v, ok := s.Attr(ns.Word14, "val"); ok {
						if c, ok := theme[drawingNames[string(v)]]; ok {
							rp.color = c
						}
					}
				case "gradFill":
					rp.color = unknown
				case "lumMod", "lumOff", "tint", "shade", "satMod", "satOff", "hueMod", "hueOff":
					rp.color = unknown // an adjusted colour is not computed
				}
			}
			continue
		}
		if s.Space() != ns.Word {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "vanish":
			rp.setFlag(propVanish, onOff(s))
		case "b":
			rp.setFlag(propBold, onOff(s))
		case "i":
			rp.setFlag(propItalic, onOff(s))
		case "strike", "dstrike":
			rp.setFlag(propStrike, onOff(s))
		case "u":
			v, _ := s.Attr(ns.Word, "val")
			rp.setFlag(propUnderline, string(v) != "none")
		case "sz":
			if v, ok := intAttr(s, "val"); ok && v > 0 {
				rp.size = int16(min(v, 30000))
			}
		case "vertAlign":
			v, _ := s.Attr(ns.Word, "val")
			switch string(v) {
			case "superscript":
				rp.vert = vertSuper
			case "subscript":
				rp.vert = vertSub
			default:
				rp.vert = vertBaseline
			}
		case "color":
			rp.color = readColor(s, "val", "themeColor", theme)
		case "shd":
			rp.shade = readShade(s, theme)
		case "w":
			// The width of the characters, as a percentage of normal.
			if v, ok := intAttr(s, "val"); ok && v > 0 {
				rp.scale = int16(min(v, 30000))
			}
		case "fitText":
			// The run is squeezed into a given width, in twentieths of a
			// point.
			if v, ok := intAttr(s, "val"); ok {
				rp.setFlag(propSqueezed, float64(v)/20 < gofastcore.HiddenTinySize)
			}
		case "highlight":
			v, _ := s.Attr(ns.Word, "val")
			if c, ok := highlights[string(v)]; ok {
				rp.highlight = c
			} else {
				// "none": no highlight, whatever a style says.
				rp.highlight = color{set: true, auto: true}
			}
		case "rStyle":
			v, _ := s.Attr(ns.Word, "val")
			rp.style = string(v)
		default:
			// Everything else, including rPrChange, which holds the
			// formatting the run had before a tracked change.
			s.Skip()
		}
	}
}

// readColor reads a colour from an element such as w:color or w:shd: a
// six-digit value, "auto", or failing that a theme colour by name. Word
// writes the value it computed from the theme alongside the theme name,
// so the theme is only consulted when the value is missing. Tints and
// shades of theme colours are not applied.
func readColor(s *xmlscan.Scanner, valueAttr, themeAttr string, theme map[string]color) color {
	v, _ := s.Attr(ns.Word, valueAttr)
	if c, ok := hexColor(v); ok {
		return c
	}
	if name, ok := s.Attr(ns.Word, themeAttr); ok {
		if c, found := theme[themeNames[string(name)]]; found {
			return c
		}
	}
	if string(v) == "auto" {
		return color{set: true, auto: true}
	}
	return color{}
}

func hexColor(v []byte) (color, bool) {
	c, ok := theme.Hex(v)
	return color{set: ok, r: c.R, g: c.G, b: c.B}, ok
}

// themeNames maps the names Word uses for theme colours in a document to
// the names of the colours in the theme part.
var themeNames = map[string]string{
	"dark1": "dk1", "light1": "lt1", "dark2": "dk2", "light2": "lt2",
	"text1": "dk1", "background1": "lt1", "text2": "dk2", "background2": "lt2",
	"accent1": "accent1", "accent2": "accent2", "accent3": "accent3",
	"accent4": "accent4", "accent5": "accent5", "accent6": "accent6",
	"hyperlink": "hlink", "followedHyperlink": "folHlink",
}

// highlights are the colours of Word's text highlighter.
var highlights = map[string]color{
	"black":       {set: true, r: 0, g: 0, b: 0},
	"blue":        {set: true, r: 0, g: 0, b: 255},
	"cyan":        {set: true, r: 0, g: 255, b: 255},
	"green":       {set: true, r: 0, g: 255, b: 0},
	"magenta":     {set: true, r: 255, g: 0, b: 255},
	"red":         {set: true, r: 255, g: 0, b: 0},
	"yellow":      {set: true, r: 255, g: 255, b: 0},
	"white":       {set: true, r: 255, g: 255, b: 255},
	"darkBlue":    {set: true, r: 0, g: 0, b: 128},
	"darkCyan":    {set: true, r: 0, g: 128, b: 128},
	"darkGreen":   {set: true, r: 0, g: 128, b: 0},
	"darkMagenta": {set: true, r: 128, g: 0, b: 128},
	"darkRed":     {set: true, r: 128, g: 0, b: 0},
	"darkYellow":  {set: true, r: 128, g: 128, b: 0},
	"darkGray":    {set: true, r: 128, g: 128, b: 128},
	"lightGray":   {set: true, r: 192, g: 192, b: 192},
}

// readTheme reads the colour scheme of a theme part.
func readTheme(s *xmlscan.Scanner) (map[string]color, *theme.Scheme) {
	scheme := theme.Read(s)
	out := map[string]color{}
	for name, c := range scheme.Colors() {
		out[name] = color{set: true, r: c.R, g: c.G, b: c.B}
	}
	return out, scheme
}

// hiddenReasons says why text with the given formatting cannot be seen,
// if it cannot. background is what lies behind the paragraph: its own
// shading, its table cell's, or the page.
func (p *runProps) hiddenReasons(background color) gofastcore.Hidden {
	var h gofastcore.Hidden
	if p.is(propVanish) {
		h |= gofastcore.HiddenInvisible
	}
	if p.is(propTransparent) {
		h |= gofastcore.HiddenTransparent
	}
	// Sizes are in half-points; text with no size set is 10 points. Text
	// can also be made too small to read by narrowing its characters.
	size := 10.0
	if p.size > 0 {
		size = float64(p.size) / 2
	}
	if p.scale > 0 && p.scale < 100 {
		size = size * float64(p.scale) / 100
	}
	if size < gofastcore.HiddenTinySize || p.is(propSqueezed) {
		h |= gofastcore.HiddenTiny
	}
	if p.color.known() {
		// Nearest first: the run's own shading, a highlight, then what is
		// behind the paragraph.
		behind := background
		switch {
		case p.shade.set && !p.shade.auto:
			behind = p.shade
		case p.highlight.known():
			behind = p.highlight
		}
		if !behind.set || behind.auto {
			behind = white
		}
		if !behind.unknown && gofastcore.LowContrast(p.color.r, p.color.g, p.color.b, behind.r, behind.g, behind.b) {
			h |= gofastcore.HiddenLowContrast
		}
	}
	return h
}

// style returns the inline formatting to report for a run: what the run
// itself or its character style sets, not what its paragraph's style
// gives every run, which is not inline.
func (p *runProps) inline() gofastcore.Style {
	var st gofastcore.Style
	if p.is(propBold) {
		st |= gofastcore.StyleBold
	}
	if p.is(propItalic) {
		st |= gofastcore.StyleItalic
	}
	if p.is(propUnderline) {
		st |= gofastcore.StyleUnderline
	}
	if p.is(propStrike) {
		st |= gofastcore.StyleStrike
	}
	switch p.vert {
	case vertSuper:
		st |= gofastcore.StyleSuper
	case vertSub:
		st |= gofastcore.StyleSub
	}
	return st
}

// vmlSize reads the width and height, in points, from the style attribute
// of a legacy drawing shape ("width:1pt;height:1pt"). A dimension that is
// absent or in another unit comes back as -1.
func vmlSize(style []byte) (width, height float64) {
	width, height = -1, -1
	for _, decl := range strings.Split(string(style), ";") {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
		if name != "width" && name != "height" {
			continue
		}
		number, isPt := strings.CutSuffix(value, "pt")
		if !isPt {
			continue
		}
		v, ok := parseFloat(number)
		if !ok {
			continue
		}
		if name == "width" {
			width = v
		} else {
			height = v
		}
	}
	return width, height
}

// parseFloat reads a plain non-negative decimal number.
func parseFloat(s string) (float64, bool) {
	if s == "" || len(s) > 16 {
		return 0, false
	}
	v, frac, seenDot := 0.0, 0.1, false
	for _, c := range s {
		switch {
		case c == '.' && !seenDot:
			seenDot = true
		case c >= '0' && c <= '9' && !seenDot:
			v = v*10 + float64(c-'0')
		case c >= '0' && c <= '9':
			v += float64(c-'0') * frac
			frac /= 10
		default:
			return 0, false
		}
	}
	return v, true
}
