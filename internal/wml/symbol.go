package wml

import (
	"unicode/utf8"

	"github.com/AOShei/go-fast-ooxml/internal/symbol"
)

// symbolRune returns the character a reader sees for w:sym, a character
// given by a font and a number. In a symbol font the number is a code,
// usually in the private range U+F020 to U+F0FF, translated by the font's
// table; a code with no character is returned as U+FFFD, the replacement
// character, so that the fact that a character stood there is kept and
// the caller can report that it could not be read.
func symbolRune(font string, code rune) rune {
	if f := symbol.ByName(font); f != symbol.None {
		if c, ok := symbol.Code(code); ok {
			if r, ok := f.Rune(c); ok {
				return r
			}
		}
		return utf8.RuneError
	}
	// A character given by number in an ordinary font is that character.
	if code > 0xFF && (code < 0xE000 || code > 0xF8FF) && utf8.ValidRune(code) {
		return code
	}
	return utf8.RuneError
}
