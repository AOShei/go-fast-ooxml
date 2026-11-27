package wml

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/symbol"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What the file declares about the display of a run of text is in its
// character formatting: hidden text (w:vanish, w:specVanish), and text
// drawn with no ink (a Word 2010 text fill of nothing, or fully
// transparent). Colour, size and position are not judged. Word stores
// character formatting in layers. A run's own properties come first,
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

// On/off run properties.
const (
	propVanish uint8 = 1 << iota
	propBold
	propItalic
	propUnderline
	propStrike
	propNoInk // a Word 2010 text fill of nothing, or fully transparent
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
	has   uint8 // which on/off properties this layer sets
	on    uint8 // and to what
	vert  int8
	style string // the character style the run names
	// font is the symbol font the run's text is in (w:rFonts), if fontSet;
	// symbol.None for an ordinary font.
	font    symbol.Font
	fontSet bool
}

// under fills what p leaves unset from parent.
func (p *runProps) under(parent *runProps) {
	inherit := parent.has &^ p.has
	p.has |= inherit
	p.on |= parent.on & inherit
	if p.vert == vertUnset {
		p.vert = parent.vert
	}
	if !p.fontSet {
		p.font, p.fontSet = parent.font, parent.fontSet
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
	return p.has == 0 && p.vert == vertUnset && p.style == "" && !p.fontSet
}

// onOff reads the value of an on/off element: present without a value, or
// with "1", "true" or "on", is on.
func onOff(s *xmlscan.Scanner) bool {
	v, has := s.Attr(ns.Word, "val")
	return !has || isOn(v)
}

// readRunProps reads w:rPr into a layer.
func readRunProps(s *xmlscan.Scanner, rp *runProps) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
			continue
		}
		if s.Space() == ns.Word14 && string(s.Local()) == "textFill" {
			// Word 2010 text effects: the fill the letters are drawn
			// with, whatever w:color says. No fill, or full transparency,
			// is no ink.
			noInk := false
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start {
					continue
				}
				switch string(s.Local()) {
				case "noFill":
					noInk = true
				case "alpha":
					if v, ok := s.Attr(ns.Word14, "val"); ok {
						if n, ok := atoi(v); ok && n >= 100000 {
							noInk = true
						}
					}
				}
			}
			rp.setFlag(propNoInk, noInk)
			continue
		}
		if s.Space() != ns.Word {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "vanish", "specVanish":
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
		case "rFonts":
			// The font of the characters a document mostly has: w:ascii,
			// and w:hAnsi for the rest of Latin. A theme font is never a
			// symbol font.
			name, ok := s.Attr(ns.Word, "ascii")
			if !ok {
				name, ok = s.Attr(ns.Word, "hAnsi")
			}
			_, themed := s.Attr(ns.Word, "asciiTheme")
			switch {
			case ok:
				rp.font, rp.fontSet = symbol.ByName(string(name)), true
			case themed:
				rp.font, rp.fontSet = symbol.None, true
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

// display returns what the formatting declares about the display of the
// text: hidden, or drawn with no ink.
func (p *runProps) display() gofastcore.Display {
	var d gofastcore.Display
	if p.is(propVanish) {
		d |= gofastcore.DisplayHidden
	}
	if p.is(propNoInk) {
		d |= gofastcore.DisplayNoInk
	}
	return d
}

// inline returns the inline formatting to report for a run: what the run
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
