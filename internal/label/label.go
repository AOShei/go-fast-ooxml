// Package label writes the labels a reader sees in front of list items:
// bullets, and numbers in the forms Word and PowerPoint both use.
package label

import (
	"strings"
	"unicode"

	"github.com/AOShei/go-fast-ooxml/internal/symbol"
)

// Bullet returns the character a reader sees for a bullet definition.
// Bullets are usually stored as a code of a symbol font, often in the
// private range; the code is translated by the font's table, and a code
// with no character, or a bullet with nothing to show, becomes a plain
// bullet.
func Bullet(text, font string) string {
	// In a symbol font every character is a code, not only those in the
	// private range: "§" in Wingdings is a small square.
	f := symbol.ByName(font)
	for _, r := range text {
		code, coded := symbol.Code(r)
		switch {
		case coded && (f != symbol.None || r >= 0xF000):
			if f == symbol.None {
				return "•" // a symbol code with no symbol font named
			}
			if c, ok := f.Rune(code); ok && c != ' ' {
				return string(c)
			}
			return "•"
		case r >= 0xFB00 && r <= 0xFB06:
			continue // a ligature is not a bullet, and is not written as one
		case r == 'o' || r == '-' || r == '*' || r > 0x7F && unicode.IsPrint(r):
			return string(r)
		}
	}
	return "•"
}

// Roman writes a number from 1 to 3999 in capital Roman numerals, and ""
// for any other.
func Roman(v int) string {
	if v < 1 || v > 3999 {
		return ""
	}
	var sb strings.Builder
	for _, p := range []struct {
		n int
		s string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}} {
		for v >= p.n {
			sb.WriteString(p.s)
			v -= p.n
		}
	}
	return sb.String()
}

// Letters writes 1 as A, 26 as Z, 27 as AA, 28 as BB: Office repeats
// the letter, it does not count in base 26. It returns "" for a number it
// cannot write.
func Letters(v int) string {
	if v < 1 || v > 26*30 {
		return ""
	}
	return strings.Repeat(string(rune('A'+(v-1)%26)), (v-1)/26+1)
}
