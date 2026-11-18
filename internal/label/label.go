// Package label writes the labels a reader sees in front of list items:
// bullets, and numbers in the forms Word and PowerPoint both use.
package label

import (
	"strings"
	"unicode"
)

// Bullet returns the character a reader sees for a bullet definition.
// Bullets are usually stored as a code in a symbol font's private range;
// the common ones are translated and any other becomes a plain bullet.
func Bullet(text, font string) string {
	// In a symbol font every character is a code, not only those in the
	// private range: "§" in Wingdings is a small square.
	symbolic := false
	switch strings.ToLower(font) {
	case "symbol", "wingdings", "wingdings 2", "wingdings 3", "webdings":
		symbolic = true
	}
	for _, r := range text {
		var code rune
		switch {
		case r >= 0xF000 && r <= 0xF0FF:
			code = r & 0xFF
		case symbolic && r > 0x20 && r <= 0xFF:
			code = r
		case r >= 0xFB00 && r <= 0xFB06:
			continue // a ligature is not a bullet, and is not written as one
		case r == 'o' || r == '-' || r == '*' || r > 0x7F && unicode.IsPrint(r):
			return string(r)
		default:
			continue
		}
		switch strings.ToLower(font) {
		case "symbol":
			switch code {
			case 0xB7:
				return "•"
			case 0x2D:
				return "−"
			case 0xA8:
				return "♦"
			case 0xDE:
				return "⇒"
			}
		case "wingdings":
			switch code {
			case 0xA7:
				return "▪"
			case 0x6C, 0x9F:
				return "●"
			case 0x6E:
				return "■"
			case 0x71, 0x6F, 0xA8:
				return "□"
			case 0x76:
				return "❖"
			case 0xD8:
				return "➢"
			case 0xE0:
				return "→"
			case 0xFC:
				return "✓"
			case 0xFE:
				return "☑"
			}
		}
		return "•"
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
