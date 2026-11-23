// Package xlsb reads the parts of an Excel binary workbook (.xlsb,
// [MS-XLSB]): the same package as an .xlsx file, whose workbook, sheets,
// shared strings, styles, comments and tables are records in place of
// XML. A record is a type, a size and that many bytes; this package
// splits a part into records and reads the fields of one.
package xlsb

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"unicode/utf16"
	"unicode/utf8"
)

// Errors of a part whose records cannot be read to its end.
var (
	// ErrTruncated: the part ends inside a record.
	ErrTruncated = errors.New("xlsb: the part ends inside a record")
	// ErrRecordTooLarge: a record says it is larger than any this
	// package reads.
	ErrRecordTooLarge = errors.New("xlsb: a record larger than 16 MiB")
	// ErrBadHeader: a record's type or size is not written as the format
	// writes them.
	ErrBadHeader = errors.New("xlsb: a record header that cannot be read")
)

// MaxRecord bounds the size of a record. Excel's largest are well under
// it: a cell holds at most 32,767 characters, two bytes each.
const MaxRecord = 16 << 20

// Reader reads the records of a part one at a time.
type Reader struct {
	r    *bufio.Reader
	id   int
	data []byte
	err  error
}

// NewReader returns a Reader of the records in r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64<<10)}
}

// Next reads the next record, and reports whether there is one. At the
// end of the part, or on an error, it returns false; Err says which.
func (r *Reader) Next() bool {
	if r.err != nil {
		return false
	}
	// The type: one or two bytes of seven bits each, the high bit saying
	// another follows.
	b, err := r.r.ReadByte()
	if err != nil {
		if err != io.EOF {
			r.err = err
		} else {
			r.err = io.EOF
		}
		return false
	}
	id := int(b & 0x7F)
	if b&0x80 != 0 {
		b, err = r.r.ReadByte()
		if err != nil {
			r.fail(err)
			return false
		}
		if b&0x80 != 0 {
			r.err = ErrBadHeader
			return false
		}
		id |= int(b) << 7
	}
	// The size: one to four bytes of seven bits each.
	size := 0
	for i := 0; ; i++ {
		b, err = r.r.ReadByte()
		if err != nil {
			r.fail(err)
			return false
		}
		size |= int(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			break
		}
		if i == 3 {
			r.err = ErrBadHeader
			return false
		}
	}
	if size > MaxRecord {
		r.err = ErrRecordTooLarge
		return false
	}
	// The bytes, read as they come rather than all at once: a record that
	// claims more than the part holds costs no more than the part.
	r.data = r.data[:0]
	for size > 0 {
		n := min(size, 64<<10)
		at := len(r.data)
		r.data = append(r.data, make([]byte, n)...)
		got, err := io.ReadFull(r.r, r.data[at:])
		if err != nil {
			r.data = r.data[:at+got]
			r.fail(err)
			return false
		}
		size -= n
	}
	r.id = id
	return true
}

// fail records an error inside a record: the end of the part there is a
// part cut short; anything else is what the part's reader said.
func (r *Reader) fail(err error) {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = ErrTruncated
	}
	r.err = err
}

// ID is the type of the current record.
func (r *Reader) ID() int { return r.id }

// Data is the content of the current record, valid until the next call
// of Next.
func (r *Reader) Data() []byte { return r.data }

// Err returns the error that stopped the reading, or nil at the end of
// the part.
func (r *Reader) Err() error {
	if r.err == io.EOF {
		return nil
	}
	return r.err
}

// Fields reads the fields of a record in order. Reading past its end
// gives zeros and sets Short, so a record cut short in a damaged file
// reads as far as it goes.
type Fields struct {
	b     []byte
	Short bool
}

// NewFields returns a reader of the fields in b.
func NewFields(b []byte) Fields { return Fields{b: b} }

// Left is the number of bytes not yet read.
func (f *Fields) Left() int { return len(f.b) }

func (f *Fields) take(n int) []byte {
	if n < 0 || len(f.b) < n {
		f.Short = true
		f.b = nil
		return nil
	}
	v := f.b[:n]
	f.b = f.b[n:]
	return v
}

// Bytes reads n bytes.
func (f *Fields) Bytes(n int) []byte { return f.take(n) }

// Formula reads a formula as the format writes it (CellParsedFormula,
// NameParsedFormula and the like): its tokens, then the extra bytes some
// tokens take their data from.
func (f *Fields) Formula() (rgce, extra []byte) {
	rgce = f.take(int(f.U32()))
	extra = f.take(int(f.U32()))
	return rgce, extra
}

// Skip passes over n bytes.
func (f *Fields) Skip(n int) { f.take(n) }

// U8 reads a byte.
func (f *Fields) U8() uint8 {
	if b := f.take(1); b != nil {
		return b[0]
	}
	return 0
}

// U16 reads a little-endian 16-bit number.
func (f *Fields) U16() uint16 {
	if b := f.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

// U32 reads a little-endian 32-bit number.
func (f *Fields) U32() uint32 {
	if b := f.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

// I32 reads a little-endian signed 32-bit number.
func (f *Fields) I32() int32 { return int32(f.U32()) }

// F64 reads a little-endian IEEE double.
func (f *Fields) F64() float64 {
	if b := f.take(8); b != nil {
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	}
	return 0
}

// String reads a string as the format writes most (XLWideString,
// XLNullableWideString): a 32-bit count of UTF-16 code units, then the
// units, appending it to dst as UTF-8. A count of 0xFFFFFFFF is the
// nullable string's "no string", and gives nothing.
func (f *Fields) String(dst []byte) []byte {
	n := f.U32()
	if n == 0xFFFFFFFF || f.Short {
		return dst
	}
	if uint64(n)*2 > uint64(len(f.b)) {
		// Longer than the record: what there is of it.
		n = uint32(len(f.b) / 2)
		f.Short = true
	}
	return AppendUTF16(dst, f.take(int(n)*2))
}

// RichString reads a string with its formatting (RichStr), appending its
// text: a byte of flags, the string, then runs of formatting and a
// reading aid for East Asian text, which are left out.
func (f *Fields) RichString(dst []byte) []byte {
	f.U8()
	return f.String(dst)
}

// StrRun is a run of a rich string (StrRun): from the character Ich, in
// 16-bit units, the text is in the font numbered Font.
type StrRun struct{ Ich, Font uint16 }

// RichStringRuns reads a rich string as RichString does, and appends its
// runs to runs: those that begin in order, within the string.
func (f *Fields) RichStringRuns(dst []byte, runs []StrRun) ([]byte, []StrRun) {
	flags := f.U8()
	before := len(f.b)
	dst = f.String(dst)
	units := int64(max(before-len(f.b)-4, 0) / 2) // the string's 16-bit units
	if flags&1 == 0 || f.Short {
		return dst, runs
	}
	n := f.U32()
	last := -1
	for i := uint32(0); i < n && len(f.b) >= 4; i++ {
		r := StrRun{Ich: f.U16(), Font: f.U16()}
		if int(r.Ich) <= last || int64(r.Ich) >= units {
			continue
		}
		last = int(r.Ich)
		runs = append(runs, r)
	}
	return dst, runs
}

// AppendUTF16 appends little-endian UTF-16 text to dst as UTF-8. Half of
// a surrogate pair is not a character, and is written as U+FFFD.
func AppendUTF16(dst, b []byte) []byte {
	for i := 0; i+1 < len(b); i += 2 {
		u := rune(binary.LittleEndian.Uint16(b[i:]))
		switch {
		case u < 0x80:
			dst = append(dst, byte(u))
			continue
		case utf16.IsSurrogate(u):
			if u < 0xDC00 && i+3 < len(b) {
				if lo := rune(binary.LittleEndian.Uint16(b[i+2:])); lo >= 0xDC00 && lo <= 0xDFFF {
					dst = utf8.AppendRune(dst, utf16.DecodeRune(u, lo))
					i += 2
					continue
				}
			}
			u = utf8.RuneError
		}
		dst = utf8.AppendRune(dst, u)
	}
	return dst
}

// Range reads a rectangle of cells (RfX, UncheckedRfX): first and last
// row, then first and last column, from 0.
func (f *Fields) Range() (r0, r1, c0, c1 int32) {
	return f.I32(), f.I32(), f.I32(), f.I32()
}

// RK decodes a number written in the compact form (RkNumber): a 30-bit
// whole number or the high 30 bits of a double, and a flag that it is to
// be divided by 100.
func RK(v uint32) float64 {
	var x float64
	if v&2 != 0 {
		x = float64(int32(v) >> 2)
	} else {
		x = math.Float64frombits(uint64(v&^3) << 32)
	}
	if v&1 != 0 {
		x /= 100
	}
	return x
}

// ErrorText is what a cell shows for an error code (BErr).
func ErrorText(code byte) string {
	switch code {
	case 0x00:
		return "#NULL!"
	case 0x07:
		return "#DIV/0!"
	case 0x0F:
		return "#VALUE!"
	case 0x17:
		return "#REF!"
	case 0x1D:
		return "#NAME?"
	case 0x24:
		return "#NUM!"
	case 0x2A:
		return "#N/A"
	case 0x2B:
		return "#GETTING_DATA"
	}
	return ""
}
