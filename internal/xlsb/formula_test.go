package xlsb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"unicode/utf16"
)

func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
func u16(v uint16) []byte         { return binary.LittleEndian.AppendUint16(nil, v) }
func u32(v uint32) []byte         { return binary.LittleEndian.AppendUint32(nil, v) }
func f64(v float64) []byte        { return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v)) }

func str(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := join([]byte{0x17}, u16(uint16(len(units))))
	for _, u := range units {
		out = append(out, u16(u)...)
	}
	return out
}

func cellRef(op byte, row uint32, col uint16) []byte { return join([]byte{op}, u32(row), u16(col)) }
func area(op byte, r0, r1 uint32, c0, c1 uint16) []byte {
	return join([]byte{op}, u32(r0), u32(r1), u16(c0), u16(c1))
}
func integer(v uint16) []byte { return join([]byte{0x1E}, u16(v)) }

const rel = 0xC000 // both the row and the column relative

func TestFormula(t *testing.T) {
	book := &Book{
		Names:  []string{"Rate", "_xlnm.Print_Area", ""},
		Sheets: []string{"Data", "My sheet", "2024", "A1", "R1C1", "TRUE", "it's"},
		Links:  []Link{LinkSelf, LinkOther, LinkSame},
		XTI:    []XTI{{0, 0, 0}, {0, 1, 1}, {0, 0, 1}, {1, 0, 0}, {2, 0, 0}, {0, 2, 2}, {0, 3, 3}, {0, 4, 4}, {0, 5, 5}, {0, 6, 6}, {0, 9, 9}},
	}
	at := Place{Row: 4, Col: 2} // C5
	for _, c := range []struct {
		name        string
		rgce, extra []byte
		want        string // "" for a formula not read back
	}{
		{"sum", join(area(0x25, 0, 1, rel, 1|rel), []byte{0x42, 1}, u16(4)), nil, "SUM(A1:B2)"},
		{"operators", join(integer(1), integer(2), integer(3), []byte{0x05, 0x03}), nil, "1+2*3"},
		{"parentheses", join(integer(1), integer(2), []byte{0x03, 0x15}, integer(3), []byte{0x05}), nil, "(1+2)*3"},
		{"comparisons", join(cellRef(0x44, 0, rel), integer(0), []byte{0x0E}), nil, "A1<>0"},
		{"unary and percent", join(integer(5), []byte{0x13, 0x14}), nil, "-5%"},
		{"text joined", join(str(`say "hi"`), cellRef(0x44, 0, 0), []byte{0x08}), nil, `"say ""hi"""&$A$1`},
		{"numbers", join(join([]byte{0x1F}, f64(0.1)), join([]byte{0x1F}, f64(1e21)), join([]byte{0x1F}, f64(-2.5e-10)), []byte{0x42, 3}, u16(4)), nil, "SUM(0.1,1E+21,-2.5E-10)"},
		{"truth and errors", join([]byte{0x1D, 1}, []byte{0x1C, 0x2A}, []byte{0x42, 2}, u16(4)), nil, "SUM(TRUE,#N/A)"},
		{"missing argument", join(cellRef(0x44, 0, rel), []byte{0x16}, integer(1), []byte{0x42, 3}, u16(1)), nil, "IF(A1,,1)"},
		{"fixed arguments", join(integer(2), []byte{0x41}, u16(20)), nil, "SQRT(2)"},
		{"if, with its jumps", join(cellRef(0x44, 0, rel), []byte{0x19, 0x02, 4, 0}, integer(1), []byte{0x19, 0x08, 3, 0}, integer(2), []byte{0x19, 0x08, 3, 0}, []byte{0x42, 3}, u16(1)), nil, "IF(A1,1,2)"},
		{"choose, with its jumps", join(integer(1), []byte{0x19, 0x04}, u16(1), u16(0), u16(0), integer(7), []byte{0x42, 2}, u16(100)), nil, "CHOOSE(1,7)"},
		{"sum of one", join(area(0x25, 0, 4, rel, rel), []byte{0x19, 0x10, 0, 0}), nil, "SUM(A1:A5)"},
		{"spaces before an operand", join(integer(1), []byte{0x19, 0x40, 0, 1}, integer(2), []byte{0x03}), nil, "1+ 2"},
		{"spaces before an operator", join(integer(1), integer(2), []byte{0x19, 0x40, 0, 2}, []byte{0x03}), nil, "1  +2"},
		{"a line break before a function", join(integer(1), []byte{0x19, 0x40, 1, 1}, []byte{0x42, 1}, u16(4)), nil, "\nSUM(1)"},
		{"spaces inside its parentheses", join(integer(1), []byte{0x19, 0x40, 2, 1}, []byte{0x19, 0x40, 4, 2}, []byte{0x42, 1}, u16(4)), nil, "SUM (1  )"},
		{"whole columns", area(0x25, 0, 0xFFFFF, 1|rel, 2), nil, "B:$C"},
		{"whole rows", area(0x25, 2, 3, 0, 0x3FFF), nil, "$3:$4"},
		{"a name", join([]byte{0x43}, u32(1), integer(2), []byte{0x05}), nil, "Rate*2"},
		{"another sheet", cellRef(0x3A, 0, 0), nil, ""},
		{"3-D", join([]byte{0x3A}, u16(1), u32(0), u16(rel)), nil, "'My sheet'!A1"},
		{"3-D range of sheets", join([]byte{0x3B}, u16(2), u32(0), u32(1), u16(0), u16(1)), nil, "'Data:My sheet'!$A$1:$B$2"},
		{"sheets named like other things", join(join([]byte{0x3A}, u16(5), u32(0), u16(rel)), join([]byte{0x3A}, u16(6), u32(0), u16(rel)), join([]byte{0x3A}, u16(7), u32(0), u16(rel)), join([]byte{0x3A}, u16(8), u32(0), u16(rel)), join([]byte{0x3A}, u16(9), u32(0), u16(rel)), []byte{0x42, 5}, u16(4)), nil,
			"SUM('2024'!A1,'A1'!A1,'R1C1'!A1,'TRUE'!A1,'it''s'!A1)"},
		{"a sheet that is gone", join([]byte{0x3A}, u16(10), u32(0), u16(rel)), nil, "#REF!A1"},
		{"this sheet", join([]byte{0x3A}, u16(4), u32(0), u16(rel)), nil, "A1"},
		{"another file", join([]byte{0x3A}, u16(3), u32(0), u16(rel)), nil, ""},
		{"deleted", join([]byte{0x2A}, make([]byte, 6), []byte{0x3C}, u16(0), make([]byte, 6), []byte{0x10}), nil, "#REF!,Data!#REF!"},
		{"shared, relative to where it is used", join(cellRef(0x2C, 0xFFFFF, 0x3FFF|rel), cellRef(0x2C, 1, 0|0x4000), []byte{0x03}), nil, "B4+C$2"},
		{"shared, past the first row", cellRef(0x2C, 0xFFFFA, rel), nil, "C1048575"},
		{"array", join([]byte{0x60}, make([]byte, 14)), join(u32(2), u32(2), []byte{0}, f64(1), []byte{1}, u16(1), u16('x'), []byte{2, 1}, []byte{4, 7, 0, 0, 0}), `{1,"x";TRUE,#DIV/0!}`},
		{"a name through the links", join([]byte{0x39}, u16(0), u32(2)), nil, "_xlnm.Print_Area"},
		{"a name with no name", join([]byte{0x43}, u32(3)), nil, ""},
		{"a name of another file", join([]byte{0x39}, u16(3), u32(1)), nil, ""},
		{"a function not known", join(integer(1), []byte{0x41}, u16(0x7000)), nil, ""},
		{"a function named by its first argument", join([]byte{0x43}, u32(1), integer(1), []byte{0x42, 2}, u16(0xFF)), nil, "Rate(1)"},
		{"too few operands", []byte{0x03}, nil, ""},
		{"two left over", join(integer(1), integer(2)), nil, ""},
		{"cut short", join(integer(1), []byte{0x1F, 0, 0}), nil, ""},
		{"a table's column", []byte{0x18, 0x19, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, nil, ""},
		{"empty", nil, nil, ""},
	} {
		got, ok := Formula(c.rgce, c.extra, book, at)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
}

func TestRK(t *testing.T) {
	for _, c := range []struct {
		v    uint32
		want float64
	}{
		{42<<2 | 2, 42},
		{0xFFFFFFE6, -7}, // -7, two bits up, and the flag of a whole number
		{150<<2 | 3, 1.5},
		{uint32(math.Float64bits(2.5) >> 32), 2.5},
		{uint32(math.Float64bits(12.5)>>32) | 1, 0.125},
	} {
		if got := RK(c.v); got != c.want {
			t.Errorf("RK(%#x) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestStrings(t *testing.T) {
	units := []uint16{'a', 0xD83D, 0xDE00, 0xD800, 'b', 0xDC00}
	var b []byte
	for _, u := range units {
		b = append(b, u16(u)...)
	}
	if got := string(AppendUTF16(nil, b)); got != "a\U0001F600�b�" {
		t.Errorf("%q", got)
	}
	// A string longer than its record is what there is of it.
	f := NewFields(join(u32(10), u16('h'), u16('i')))
	if got := string(f.String(nil)); got != "hi" || !f.Short {
		t.Errorf("%q, short %v", got, f.Short)
	}
	// No string at all.
	f = NewFields(u32(0xFFFFFFFF))
	if got := f.String(nil); len(got) != 0 || f.Short {
		t.Errorf("%q, short %v", got, f.Short)
	}
}

func TestReader(t *testing.T) {
	// A type of two bytes, a size of two.
	data := join([]byte{0x83, 0x01, 0x80, 0x01}, make([]byte, 128), []byte{0x13, 0x01, 'x'})
	r := NewReader(bytes.NewReader(data))
	if !r.Next() || r.ID() != 131 || len(r.Data()) != 128 {
		t.Fatalf("first: %d %d %v", r.ID(), len(r.Data()), r.Err())
	}
	if !r.Next() || r.ID() != 19 || string(r.Data()) != "x" {
		t.Fatalf("second: %d %q %v", r.ID(), r.Data(), r.Err())
	}
	if r.Next() || r.Err() != nil {
		t.Fatalf("end: %v", r.Err())
	}
	for name, c := range map[string]struct {
		data []byte
		err  error
	}{
		"cut inside": {[]byte{0x13, 0x05, 'a'}, ErrTruncated},
		"too large":  {[]byte{0x13, 0xFF, 0xFF, 0xFF, 0x7F}, ErrRecordTooLarge},
		"type":       {[]byte{0x93, 0x93, 0x01}, ErrBadHeader},
		"size":       {[]byte{0x13, 0x80, 0x80, 0x80, 0x80, 0x01}, ErrBadHeader},
	} {
		r := NewReader(bytes.NewReader(c.data))
		for r.Next() {
		}
		if !errors.Is(r.Err(), c.err) {
			t.Errorf("%s: %v, want %v", name, r.Err(), c.err)
		}
	}
}
