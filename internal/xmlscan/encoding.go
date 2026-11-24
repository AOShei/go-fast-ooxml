package xmlscan

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/AOShei/go-fast-ooxml/internal/codepage"
)

// sniff looks at the first bytes of the input, skips a byte order mark and
// arranges for input that is not UTF-8 to be converted on the way in. Office
// Open XML requires UTF-8 or UTF-16; Latin-1 and Windows-1252 are accepted
// as well because files in the wild declare them.
func (s *Scanner) sniff() {
	for s.end-s.pos < 5 && s.more() { // enough to recognize "<?xml"
	}
	if s.err != nil {
		return
	}
	b := s.buf[s.pos:s.end]
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		s.pos += 3
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		s.pos += 2
		s.convert(func(src io.Reader) io.Reader { return &utf16Reader{src: src} })
		return
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		s.pos += 2
		s.convert(func(src io.Reader) io.Reader { return &utf16Reader{src: src, big: true} })
		return
	case len(b) >= 4 && b[0] == '<' && b[1] == 0 && b[3] == 0:
		s.convert(func(src io.Reader) io.Reader { return &utf16Reader{src: src} })
		return
	case len(b) >= 4 && b[0] == 0 && b[1] == '<' && b[2] == 0:
		s.convert(func(src io.Reader) io.Reader { return &utf16Reader{src: src, big: true} })
		return
	}

	// An XML declaration may name a single-byte encoding. It is "<?xml"
	// followed by white space; "<?xml-stylesheet" and the like are not it.
	for s.end-s.pos < 6 && s.more() {
	}
	if b = s.buf[s.pos:s.end]; len(b) < 6 || !bytes.HasPrefix(b, []byte("<?xml")) || !isSpace(b[5]) {
		return
	}
	for bytes.Index(s.buf[s.pos:s.end], []byte("?>")) < 0 && s.end-s.pos < 1024 && s.more() {
	}
	if s.err != nil {
		return
	}
	decl := s.buf[s.pos:s.end]
	if i := bytes.Index(decl, []byte("?>")); i >= 0 {
		decl = decl[:i]
	} else {
		return
	}
	enc := declaredEncoding(decl)
	is := func(names ...string) bool {
		for _, n := range names {
			if len(enc) == len(n) && bytes.EqualFold(enc, []byte(n)) {
				return true
			}
		}
		return false
	}
	switch {
	case len(enc) == 0, is("utf-8", "utf8", "us-ascii", "ascii", "utf-16", "utf-16le", "utf-16be"):
		// UTF-16 named in the declaration of input that reads as ASCII is
		// a mislabel: the bytes are what count.
	case is("iso-8859-1", "latin1", "latin-1", "l1"):
		s.convert(func(src io.Reader) io.Reader { return &singleByteReader{src: src} })
	case is("windows-1252", "cp1252", "cp-1252"):
		s.convert(func(src io.Reader) io.Reader { return &singleByteReader{src: src, cp1252: true} })
	case is("iso-8859-15", "latin-9", "latin9", "l9"):
		s.convert(func(src io.Reader) io.Reader { return &singleByteReader{src: src, latin9: true} })
	default:
		// The other code pages of Windows, which programs other than
		// Office write their XML in.
		if cp, ok := codepage.ByName(string(enc)); ok {
			s.convert(func(src io.Reader) io.Reader { return &codePageReader{src: src, cp: cp} })
			return
		}
		s.fail(fmt.Errorf("%w: %q", ErrEncoding, enc))
	}
}

// declaredEncoding returns the value of the encoding pseudo-attribute of an
// XML declaration, or nil. It does not allocate: every part has one.
func declaredEncoding(decl []byte) []byte {
	i := bytes.Index(decl, []byte("encoding"))
	if i < 1 || !isSpace(decl[i-1]) {
		return nil
	}
	rest := bytes.TrimLeft(decl[i+len("encoding"):], " \t\r\n")
	if len(rest) == 0 || rest[0] != '=' {
		return nil
	}
	rest = bytes.TrimLeft(rest[1:], " \t\r\n")
	if len(rest) == 0 || (rest[0] != '"' && rest[0] != '\'') {
		return nil
	}
	end := bytes.IndexByte(rest[1:], rest[0])
	if end < 0 {
		return nil
	}
	return rest[1 : 1+end]
}

// convert puts a converting reader between the input and the window. What
// is already in the window is fed through it first.
func (s *Scanner) convert(wrap func(io.Reader) io.Reader) {
	pending := append([]byte(nil), s.buf[s.pos:s.end]...)
	var src io.Reader = bytes.NewReader(pending)
	if s.r != nil && !s.eof {
		src = io.MultiReader(src, s.r)
	}
	if !s.own {
		s.buf, s.own = make([]byte, DefaultBufferSize), true
	}
	s.r, s.pos, s.end, s.base, s.eof = wrap(src), 0, 0, 0, false
	s.converted = true
}

// UTF8 returns the input as the scanner reads it: converted to UTF-8 from
// the encoding its byte order mark or declaration names, without the
// mark. converted reports that it was not UTF-8. The error is
// ErrEncoding, for an encoding the scanner does not read, or the
// reader's.
func UTF8(r io.Reader) (utf8 io.Reader, converted bool, err error) {
	s := &Scanner{}
	s.reset(nil)
	s.r, s.own, s.buf = r, true, make([]byte, 4096)
	s.sniff()
	if s.err != nil {
		return nil, false, s.err
	}
	pending := append([]byte(nil), s.buf[s.pos:s.end]...)
	utf8 = bytes.NewReader(pending)
	if s.r != nil && !s.eof {
		utf8 = io.MultiReader(utf8, s.r)
	}
	return utf8, s.converted, nil
}

// utf16Reader converts UTF-16 to UTF-8.
type utf16Reader struct {
	src   io.Reader
	big   bool
	in    [4096]byte
	carry int  // bytes of an incomplete code unit at the start of in
	high  rune // a high surrogate waiting for its pair, or 0
	out   []byte
	off   int
	err   error
}

func (u *utf16Reader) Read(p []byte) (int, error) {
	for u.off == len(u.out) {
		if u.err != nil {
			return 0, u.err
		}
		u.out, u.off = u.out[:0], 0
		n, err := u.src.Read(u.in[u.carry:])
		n += u.carry
		i := 0
		for ; i+1 < n; i += 2 {
			c := rune(u.in[i]) | rune(u.in[i+1])<<8
			if u.big {
				c = rune(u.in[i])<<8 | rune(u.in[i+1])
			}
			if u.high != 0 {
				high := u.high
				u.high = 0
				if c >= 0xDC00 && c <= 0xDFFF {
					u.out = utf8.AppendRune(u.out, utf16.DecodeRune(high, c))
					continue
				}
				u.out = utf8.AppendRune(u.out, utf8.RuneError) // unpaired high surrogate
			}
			switch {
			case c >= 0xD800 && c <= 0xDBFF:
				u.high = c
			case c >= 0xDC00 && c <= 0xDFFF:
				u.out = utf8.AppendRune(u.out, utf8.RuneError) // unpaired low surrogate
			default:
				u.out = utf8.AppendRune(u.out, c)
			}
		}
		u.carry = copy(u.in[:], u.in[i:n])
		if n == u.carry && err == nil {
			return 0, nil // the source made no progress; let the caller decide
		}
		if err != nil {
			u.err = err
			if u.carry > 0 || u.high != 0 {
				u.out = utf8.AppendRune(u.out, utf8.RuneError) // input ended mid-character
				u.carry, u.high = 0, 0
			}
		}
	}
	n := copy(p, u.out[u.off:])
	u.off += n
	return n, nil
}

// latin9 holds the characters of ISO 8859-15 where it differs from ISO
// 8859-1.
var latin9 = [256]rune{0xA4: 0x20AC, 0xA6: 0x0160, 0xA8: 0x0161, 0xB4: 0x017D, 0xB8: 0x017E, 0xBC: 0x0152, 0xBD: 0x0153, 0xBE: 0x0178}

// codePageReader converts text in a Windows code page to UTF-8. A lead
// byte of a double-byte code page that ends a read waits for its trail
// byte in the next.
type codePageReader struct {
	src   io.Reader
	cp    int
	in    [4096]byte
	carry int // bytes of in kept from the read before
	out   []byte
	off   int
	err   error
}

func (c *codePageReader) Read(p []byte) (int, error) {
	for c.off == len(c.out) {
		if c.err != nil {
			return 0, c.err
		}
		c.out, c.off = c.out[:0], 0
		n, err := c.src.Read(c.in[c.carry:])
		n += c.carry
		// Where the last character begins: a lead byte at the end waits
		// for its trail, unless the input has ended.
		end := n
		for i := 0; i < n; i++ {
			if codepage.Lead(c.cp, c.in[i]) {
				if i+1 == n && err == nil {
					end = i
					break
				}
				i++
			}
		}
		text, _ := codepage.Decode(c.cp, c.in[:end])
		c.out = append(c.out, text...)
		c.carry = copy(c.in[:], c.in[end:n])
		if err != nil {
			c.err = err
		} else if n == c.carry && n > 0 && len(c.out) == 0 {
			return 0, nil // no progress; let the caller decide
		}
	}
	n := copy(p, c.out[c.off:])
	c.off += n
	return n, nil
}

// singleByteReader converts ISO 8859-1, or Windows-1252, to UTF-8.
type singleByteReader struct {
	src    io.Reader
	cp1252 bool
	latin9 bool // ISO 8859-15: ISO 8859-1 with eight characters replaced
	in     [4096]byte
	out    []byte
	off    int
	err    error
}

// cp1252High is Windows-1252 for bytes 0x80 to 0x9F, where it differs from
// ISO 8859-1. Unassigned bytes map to the control character of the same
// number, as Windows does.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func (l *singleByteReader) Read(p []byte) (int, error) {
	for l.off == len(l.out) {
		if l.err != nil {
			return 0, l.err
		}
		l.out, l.off = l.out[:0], 0
		n, err := l.src.Read(l.in[:])
		for _, c := range l.in[:n] {
			switch {
			case c < 0x80:
				l.out = append(l.out, c)
			case l.cp1252 && c < 0xA0:
				l.out = utf8.AppendRune(l.out, cp1252High[c-0x80])
			case l.latin9 && latin9[c] != 0:
				l.out = utf8.AppendRune(l.out, latin9[c])
			default:
				l.out = utf8.AppendRune(l.out, rune(c))
			}
		}
		l.err = err
		if n == 0 && err == nil {
			return 0, nil // the source made no progress; let the caller decide
		}
	}
	n := copy(p, l.out[l.off:])
	l.off += n
	return n, nil
}
