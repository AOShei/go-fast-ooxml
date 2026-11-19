// Package clean turns bytes read from a file into strings that obey the
// text conventions of the shared document model: valid UTF-8, no control
// characters other than line feed and tab, and for prose no surrounding
// white space and no presentation-form ligatures.
//
// Content is kept where it can be: what is changed is how it is written.
// Line and page separators become line feeds, ligatures become their
// letters. The only thing removed is control characters that have no
// meaning as text, and they are counted so the caller can report it.
package clean

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
)

// Flags select the conventions to apply beyond validity and control
// characters, which always apply.
type Flags uint8

const (
	// Trim removes leading and trailing white space.
	Trim Flags = 1 << iota
	// Ligatures expands U+FB00 to U+FB06 to their letters.
	Ligatures

	// Prose is what block text and table cells need.
	Prose = Trim | Ligatures
	// Verbatim is for code: nothing beyond validity and control characters.
	Verbatim Flags = 0
)

// String returns b as a string that obeys the conventions selected by
// flags, and the number of control characters it had to remove.
func String(b []byte, flags Flags) (s string, removed int) {
	// The usual case needs no work: printable ASCII with nothing to trim.
	if Plain(b) {
		if flags&Trim != 0 {
			return string(trimASCII(b)), 0
		}
		return string(b), 0
	}
	out, removed := Append(make([]byte, 0, len(b)), b, flags)
	if flags&Trim != 0 {
		return string(bytes.TrimFunc(out, unicode.IsSpace)), removed
	}
	return string(out), removed
}

// Plain reports whether b is printable ASCII, with line feeds and tabs:
// text that obeys the conventions as it stands, apart from trimming.
func Plain(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 || (c < 0x20 && c != '\n' && c != '\t') || c == 0x7F {
			return false
		}
	}
	return true
}

// Append appends b to dst in the form the conventions ask for, and returns
// the extended slice and the number of control characters it removed. It
// does not trim: a caller that builds a paragraph out of pieces trims the
// whole once, and the offsets of the pieces stay true until then.
func Append(dst, b []byte, flags Flags) ([]byte, int) {
	removed := 0
	for i := 0; i < len(b); {
		// Most text is printable ASCII, which is kept as it is: a run of
		// it is found eight bytes at a time and copied in one piece.
		if n := printable(b[i:]); n > 0 {
			dst = append(dst, b[i:i+n]...)
			i += n
			if i == len(b) {
				break
			}
		}
		c := b[i]
		if c < 0x80 {
			i++
			switch {
			case c == '\r':
				// Carriage returns reach here from character references;
				// a pair with a line feed is one line end.
				if i < len(b) && b[i] == '\n' {
					i++
				}
				dst = append(dst, '\n')
			case c == '\v' || c == '\f':
				dst = append(dst, '\n') // line and page separators are line ends
			case (c < 0x20 && c != '\n' && c != '\t') || c == 0x7F:
				removed++
			default:
				dst = append(dst, c)
			}
			continue
		}
		r, size := utf8.DecodeRune(b[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			dst = utf8.AppendRune(dst, utf8.RuneError) // an invalid byte
		case r == 0x85 || r == 0x2028 || r == 0x2029:
			dst = append(dst, '\n') // next line, line separator, paragraph separator
		case r < 0xA0:
			removed++ // a C1 control character
		case flags&Ligatures != 0 && r >= 0xFB00 && r <= 0xFB06:
			dst = append(dst, gofastcore.Ligature(r)...) // the letters, by the series' rule
		default:
			dst = utf8.AppendRune(dst, r)
		}
	}
	return dst, removed
}

// maxLabel bounds a label. Names and messages are short; a longer one is
// a file trying to be expensive.
const maxLabel = 512

// printable returns the length of the run of printable ASCII (0x20 to
// 0x7E) that b begins with.
func printable(b []byte) int {
	const ones, highs = 0x0101010101010101, 0x8080808080808080
	n := 0
	for len(b)-n >= 8 {
		x := binary.LittleEndian.Uint64(b[n:])
		// A byte with its high bit set, a byte under 0x20, or 0x7F: the
		// last two by the borrow of a subtraction, which is exact for
		// the lowest such byte and that is all that is asked.
		y := x ^ (0x7F * ones)
		if (x|(x-0x20*ones)|(y-ones)&^y)&highs != 0 {
			break
		}
		n += 8
	}
	for n < len(b) && b[n] >= 0x20 && b[n] < 0x7F {
		n++
	}
	return n
}

// Label makes a string that came from the file fit to appear as a name, an
// identifier or part of a message in the output: valid UTF-8, no control
// characters, no line breaks, trimmed, and of bounded length. It is for
// part names, content types, addresses and error text, which a file
// controls as much as it controls its content.
func Label(s string) string { return bounded(s, maxLabel) }

// maxAddress bounds an address. Addresses run longer than names: one with
// a query or a token in it is often over a thousand bytes, and one cut
// short leads nowhere. This is more than servers accept.
const maxAddress = 8 << 10

// Address is Label for the target of a link: the same cleaning, with room
// for the length addresses have. A caller that puts an address into the
// document counts it against the text the document may hold, since one
// address may be linked to any number of times.
func Address(s string) string { return bounded(s, maxAddress) }

func bounded(s string, limit int) string {
	plain := len(s) <= limit
	for i := 0; plain && i < len(s); i++ {
		if c := s[i]; c >= 0x80 || c < 0x20 || c == 0x7F {
			plain = false
		}
	}
	if plain {
		return strings.TrimSpace(s)
	}
	if len(s) > limit {
		s = s[:limit]
	}
	out, _ := String([]byte(s), Trim)
	out = strings.Join(strings.Fields(out), " ")
	// Cutting may have split a character; String turned the pieces into
	// U+FFFD, which says honestly that something stood there.
	return out
}

// Name is Label for a name that is text to be read, such as the name of
// an author: its ligatures are written as their letters as well.
func Name(s string) string {
	s = Label(s)
	if strings.IndexByte(s, 0xEF) < 0 {
		return s // every ligature starts with this byte
	}
	out, _ := String([]byte(s), Ligatures)
	return out
}

func trimASCII(b []byte) []byte {
	for len(b) > 0 && isASCIISpace(b[0]) {
		b = b[1:]
	}
	for len(b) > 0 && isASCIISpace(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	return b
}

func isASCIISpace(c byte) bool { return c == ' ' || c == '\n' || c == '\t' }
