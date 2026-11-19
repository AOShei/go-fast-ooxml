// Package symbol translates the characters of symbol fonts to Unicode.
//
// In a symbol font a character is a code, not a letter: "J" in Wingdings
// is a smiling face, and Office stores it as "J", or as U+F04A in the
// private range, with the font's name beside it. Without the font the text
// means nothing, so the code is translated to the character Unicode gives
// that symbol: from the Consortium's tables for Symbol, and for Wingdings,
// Wingdings 2, Wingdings 3 and Webdings from the document that encoded
// them in Unicode 7.0 (see generate.py).
package symbol

import (
	"strings"
	"unicode/utf8"
)

// Font is a symbol font, or none.
type Font uint8

const (
	// None is a font whose characters are what they say.
	None Font = iota
	Symbol
	Wingdings
	Wingdings2
	Wingdings3
	Webdings
)

// ByName returns the symbol font a typeface names, or None. Names are
// matched without regard to case or surrounding space.
func ByName(name string) Font {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "symbol":
		return Symbol
	case "wingdings":
		return Wingdings
	case "wingdings 2":
		return Wingdings2
	case "wingdings 3":
		return Wingdings3
	case "webdings":
		return Webdings
	}
	return None
}

// Code reports whether r stands for a code of a symbol font: 0x20 to 0xFF
// as typed, or U+F020 to U+F0FF, where Office puts symbol codes in the
// private range. It returns the code.
func Code(r rune) (byte, bool) {
	switch {
	case r >= 0x20 && r <= 0xFF:
		return byte(r), true
	case r >= 0xF020 && r <= 0xF0FF:
		return byte(r), true
	}
	return 0, false
}

// Rune returns the character code is in font f, and false if the font has
// no such character or Unicode none for it.
func (f Font) Rune(code byte) (rune, bool) {
	if code < 0x20 {
		return 0, false
	}
	var r rune
	switch f {
	case Symbol:
		r = symbolFont[code-0x20]
	case Wingdings:
		r = wingdings[code-0x20]
	case Wingdings2:
		r = wingdings2[code-0x20]
	case Wingdings3:
		r = wingdings3[code-0x20]
	case Webdings:
		r = webdings[code-0x20]
	}
	return r, r != 0
}

// Translate appends text to dst with every character that stands for a
// code of f replaced by the character it is in that font, and U+FFFD for a
// code that has none. Other characters are copied. It returns the new
// slice and the number of codes that had no character.
func (f Font) Translate(dst []byte, text []byte) ([]byte, int) {
	return Translate(dst, text, f, f)
}

// Translate is Font.Translate for text where two fonts apply: typed to the
// codes 0x20 to 0xFF, private to the private range U+F020 to U+F0FF, which
// DrawingML may give a font of its own (a:sym). A font that is None leaves
// its characters as they are.
func Translate(dst []byte, text []byte, typed, private Font) ([]byte, int) {
	missing := 0
	for _, r := range string(text) {
		f := typed
		if r >= 0xF000 {
			f = private
		}
		if code, ok := Code(r); ok && f != None {
			if t, ok := f.Rune(code); ok {
				r = t
			} else {
				r, missing = utf8.RuneError, missing+1
			}
		}
		dst = utf8.AppendRune(dst, r)
	}
	return dst, missing
}
