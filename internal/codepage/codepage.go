// Package codepage decodes text in the code pages of Windows (874, 932,
// 936, 949, 950, 1250 to 1258) and Mac Roman (10000), from Microsoft's
// tables of them (see generate.py). It is for text an Office file keeps in
// a code page rather than in Unicode: the source code of a VBA project.
package codepage

import (
	"strings"
	"unicode/utf8"
)

// double is a double-byte code page: the characters of single bytes from
// 0x80, and for each lead byte the run of its trail bytes.
type double struct {
	single [128]uint16
	runs   [256]run
	values []uint16
}

// run is the trail bytes of a lead byte, from lo, n of them, whose
// characters start at values[start].
type run struct {
	start int
	lo    uint8
	n     int
}

// Known reports whether the code page is one this package decodes. Code
// page 28591 (ISO 8859-1) and 65001 (UTF-8) are included.
func Known(cp int) bool {
	_, single := singles[cp]
	_, two := doubles[cp]
	return single || two || cp == 28591 || cp == 65001
}

// Decode turns text in the code page into UTF-8. A byte or pair of bytes
// the code page does not define, or text in a code page not known, is
// U+FFFD, and counted in undecoded; ASCII is always itself.
func Decode(cp int, b []byte) (s string, undecoded int) {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return string(b), 0
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/2)
	bad := func() {
		undecoded++
		sb.WriteRune(utf8.RuneError)
	}
	put := func(v uint16) {
		if v == 0 {
			bad()
			return
		}
		sb.WriteRune(rune(v))
	}
	switch {
	case cp == 65001:
		for len(b) > 0 {
			r, n := utf8.DecodeRune(b)
			if r == utf8.RuneError && n <= 1 {
				undecoded++
			}
			sb.WriteRune(r)
			b = b[n:]
		}
	case cp == 28591:
		for _, c := range b {
			sb.WriteRune(rune(c))
		}
	case singles[cp] != nil:
		table := singles[cp]
		for _, c := range b {
			if c < 0x80 {
				sb.WriteByte(c)
				continue
			}
			put(table[c-0x80])
		}
	case doubles[cp] != nil:
		d := doubles[cp]
		for i := 0; i < len(b); i++ {
			c := b[i]
			if c < 0x80 {
				sb.WriteByte(c)
				continue
			}
			r := d.runs[c]
			if r.n == 0 {
				put(d.single[c-0x80])
				continue
			}
			// A lead byte: the character is the pair.
			if i+1 >= len(b) {
				bad()
				continue
			}
			i++
			t := int(b[i]) - int(r.lo)
			if t < 0 || t >= r.n {
				bad()
				continue
			}
			put(d.values[r.start+t])
		}
	default:
		for _, c := range b {
			if c < 0x80 {
				sb.WriteByte(c)
				continue
			}
			bad()
		}
	}
	return sb.String(), undecoded
}
