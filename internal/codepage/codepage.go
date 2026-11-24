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

// Lead reports whether c is a lead byte of the double-byte code page cp:
// the first of a pair that is one character.
func Lead(cp int, c byte) bool {
	d := doubles[cp]
	return d != nil && c >= 0x80 && d.runs[c].n > 0
}

// byName maps the names XML declarations give code pages to their
// numbers, in lower case.
var byName = map[string]int{
	"windows-874": 874, "cp874": 874, "tis-620": 874,
	"shift_jis": 932, "shift-jis": 932, "sjis": 932, "x-sjis": 932, "ms932": 932, "windows-31j": 932, "cp932": 932,
	"gbk": 936, "gb2312": 936, "cp936": 936, "x-gbk": 936, "windows-936": 936,
	"euc-kr": 949, "ks_c_5601-1987": 949, "cp949": 949, "windows-949": 949,
	"big5": 950, "cp950": 950, "windows-950": 950,
	"macintosh": 10000, "x-mac-roman": 10000, "mac": 10000,
}

// ByName returns the code page an XML declaration names, if this package
// decodes it: windows-1250 to windows-1258 (also as cp1250), windows-874,
// Shift JIS, GBK, EUC-KR (as code page 949), Big5 and Mac Roman.
func ByName(name string) (int, bool) {
	name = strings.ToLower(name)
	for _, prefix := range []string{"windows-", "cp", "x-cp"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok && len(rest) == 4 && rest >= "1250" && rest <= "1258" {
			return int(rest[3]-'0') + 1250, true
		}
	}
	cp, ok := byName[name]
	return cp, ok
}
