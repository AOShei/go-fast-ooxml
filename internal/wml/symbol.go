package wml

import (
	"strings"
	"unicode/utf8"
)

// symbolRune returns the character a reader sees for a code in a symbol
// font. Word stores such characters as a font name and a number, usually
// in the private range U+F020 to U+F0FF, which means nothing without the
// font.
//
// The Symbol font is translated from the Unicode Consortium's table. A
// code in any other symbol font (Wingdings, Webdings) is returned as
// U+FFFD, the replacement character: the fact that a character stood
// there is kept, and the caller reports that it could not be read.
func symbolRune(font string, code rune) rune {
	if code >= 0xF000 && code <= 0xF0FF {
		code &= 0xFF
	}
	if strings.EqualFold(font, "Symbol") {
		if code >= 0x20 && code <= 0xFF {
			if r := symbolFont[code-0x20]; r != 0 {
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
