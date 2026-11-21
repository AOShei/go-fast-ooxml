package xlsb

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
)

// A formula in a binary part is compiled: a list of tokens (Rgce) in
// reverse Polish order, operands before the operator that takes them,
// and a second list of bytes (RgbExtra) that some tokens take their data
// from. Formula turns it back into the text Excel shows, as an .xlsx
// file holds it, without the "=".

// Book is what a formula refers to outside itself: the workbook's defined
// names, its sheets, the table of sheet references that 3-D references
// index, and its tables.
type Book struct {
	Names  []string // in the workbook's order; a formula numbers them from 1
	Sheets []string
	Links  []Link
	XTI    []XTI

	// Tables gives the workbook's tables by their number (idList). It is
	// called the first time a formula names a table's columns, from any
	// goroutine, and may be nil.
	Tables func() map[uint32]Table
	// Fields are the names of a pivot cache's fields that the formula of
	// one of its calculated fields refers to, in the order it numbers
	// them.
	Fields []string
}

// Link is one entry of the workbook's list of supporting links (SupBook):
// what the 3-D references and the names a formula takes through the links
// are to.
type Link struct {
	Kind LinkKind
	// Book is the number a formula's text gives another workbook, as in
	// [1]Sheet1!A1: the link's place among the workbook's links to other
	// files, from 1.
	Book int
	// Sheets are the sheets of another workbook.
	Sheets []string
	// Names are the defined names of another workbook; or, for an add-in,
	// the functions of it that formulas call; or, for the sheet the
	// formula is on, the names it uses (BrtPlaceholderName). A formula
	// numbers them from 1.
	Names []ExternName
}

// LinkKind is the kind of a supporting link.
type LinkKind byte

// The kinds of supporting link.
const (
	LinkOther LinkKind = iota // a DDE or OLE link, or one whose part cannot be read: not read
	LinkSelf                  // this workbook
	LinkSame                  // the sheet the formula is on
	LinkBook                  // another workbook
	LinkAddin                 // the functions of add-ins
)

// ExternName is a name a formula takes through a link.
type ExternName struct {
	Name string
	// Sheet is the sheet of another workbook the name is local to, from 1
	// in the link's Sheets; 0 for a name of the whole workbook.
	Sheet int32
}

// Table is a table of the workbook (a list object): its name as formulas
// write it, and the names of its columns in order.
type Table struct {
	Name    string
	Columns []string
}

// XTI is a reference to a range of sheets of a linked book.
type XTI struct {
	Link        int32
	First, Last int32
}

// Bounds on what a formula may come to: the stack of operands, and the
// length of the text.
const (
	maxStack   = 1 << 12
	maxFormula = 32 << 10
)

// Place is where a formula is: the cell whose formula it is, or for a
// defined name or a drop-down list the cell its relative references count
// from. A shared formula is written once with references relative to it,
// and Place is where it is used.
type Place struct{ Row, Col int32 }

// Exp is a reference from a cell to the formula it shares (PtgExp): the
// cell that holds it.
type Exp struct{ Row, Col int32 }

// ExpOf reports whether the formula is only a reference to a shared or
// array formula, and to which.
func ExpOf(rgce, extra []byte) (Exp, bool) {
	if len(rgce) != 5 || rgce[0] != 0x01 || len(extra) < 4 {
		return Exp{}, false
	}
	return Exp{int32(binary.LittleEndian.Uint32(rgce[1:])), int32(binary.LittleEndian.Uint32(extra))}, true
}

// Formula returns the text of a formula, and false if it holds anything
// this package cannot read back: a reference through a DDE or OLE link, a
// table or name the workbook does not have, a what-if table, or a
// function it does not know. Wrong text would be worse than none.
func Formula(rgce, extra []byte, book *Book, at Place) (string, bool) {
	p := parser{b: rgce, extra: extra, book: book, at: at}
	return p.run()
}

type parser struct {
	b, extra []byte
	book     *Book
	at       Place
	stack    []string
	size     int // the bytes of text on the stack
	// Spaces or line breaks the formula was typed with, waiting for the
	// token they go with: before it, before its opening parenthesis, and
	// before its closing one.
	space, open, close string
	bad                bool
}

// zeros is what a field reads as past the end of a formula: the formula
// is then not read, and the field only has to be safe to look at.
var zeros [16]byte

func short(n int) []byte {
	if n >= 0 && n <= len(zeros) {
		return zeros[:n]
	}
	return nil
}

func (p *parser) take(n int) []byte {
	if n < 0 || len(p.b) < n {
		p.bad = true
		p.b = nil
		return short(n)
	}
	v := p.b[:n]
	p.b = p.b[n:]
	return v
}

func (p *parser) takeExtra(n int) []byte {
	if n < 0 || len(p.extra) < n {
		p.bad = true
		p.extra = nil
		return short(n)
	}
	v := p.extra[:n]
	p.extra = p.extra[n:]
	return v
}

func (p *parser) u8() byte     { return p.take(1)[0] }
func (p *parser) u16() uint16  { return binary.LittleEndian.Uint16(p.take(2)) }
func (p *parser) u32() uint32  { return binary.LittleEndian.Uint32(p.take(4)) }
func (p *parser) xu32() uint32 { return binary.LittleEndian.Uint32(p.takeExtra(4)) }

func (p *parser) push(s string) {
	if p.space != "" {
		s = p.space + s
		p.space = ""
	}
	p.size += len(s)
	if len(p.stack) >= maxStack || p.size > 4*maxFormula {
		p.bad = true
		return
	}
	p.stack = append(p.stack, s)
}

func (p *parser) pop() string {
	if len(p.stack) == 0 {
		p.bad = true
		return ""
	}
	s := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	p.size -= len(s)
	return s
}

// pops takes the last n operands, in order.
func (p *parser) pops(n int) []string {
	if n > len(p.stack) {
		p.bad = true
		return nil
	}
	out := append([]string(nil), p.stack[len(p.stack)-n:]...)
	p.stack = p.stack[:len(p.stack)-n]
	for _, s := range out {
		p.size -= len(s)
	}
	return out
}

var binaryOps = [...]string{0x03: "+", 0x04: "-", 0x05: "*", 0x06: "/", 0x07: "^", 0x08: "&", 0x09: "<", 0x0A: "<=", 0x0B: "=", 0x0C: ">=", 0x0D: ">", 0x0E: "<>", 0x0F: " ", 0x10: ",", 0x11: ":"}

func (p *parser) run() (string, bool) {
	for len(p.b) > 0 && !p.bad {
		t := p.u8()
		if t < 0x20 {
			p.base(t)
		} else if t < 0x80 {
			p.classified(t)
		} else {
			p.bad = true
		}
	}
	if p.bad || len(p.stack) != 1 {
		return "", false
	}
	s := p.stack[0] + p.space + p.open + p.close
	if len(s) > maxFormula {
		return "", false
	}
	return s, true
}

func (p *parser) base(t byte) {
	switch {
	case t >= 0x03 && t <= 0x11:
		space := p.space
		p.space = ""
		b := p.pop()
		a := p.pop()
		p.push(a + space + binaryOps[t] + b)
	case t == 0x12, t == 0x13: // unary plus, minus
		space := p.space
		p.space = ""
		a := p.pop()
		p.push(space + string("+-"[t-0x12]) + a)
	case t == 0x14: // percent
		space := p.space
		p.space = ""
		p.push(p.pop() + space + "%")
	case t == 0x15: // parentheses
		space, open, close := p.space, p.open, p.close
		p.space, p.open, p.close = "", "", ""
		p.push(space + open + "(" + p.pop() + close + ")")
	case t == 0x16: // a missing argument
		p.push("")
	case t == 0x17: // a string
		n := int(p.u16())
		s := string(AppendUTF16(nil, p.take(2*n)))
		p.push(`"` + strings.ReplaceAll(s, `"`, `""`) + `"`)
	case t == 0x18: // a token whose next byte says what it is
		switch p.u8() {
		case 0x19:
			p.list()
		case 0x1D: // a field of a pivot cache, in a calculated field's formula
			i := p.u32()
			if uint64(i) >= uint64(len(p.book.Fields)) || p.book.Fields[i] == "" {
				p.bad = true
				return
			}
			name := p.book.Fields[i]
			if quoted(name) {
				name = "'" + strings.ReplaceAll(name, "'", "''") + "'"
			}
			p.push(name)
		default:
			p.bad = true
		}
	case t == 0x19:
		p.attr()
	case t == 0x1C:
		p.push(ErrorText(p.u8()))
	case t == 0x1D:
		if p.u8() != 0 {
			p.push("TRUE")
		} else {
			p.push("FALSE")
		}
	case t == 0x1E:
		p.push(strconv.Itoa(int(p.u16())))
	case t == 0x1F:
		p.push(number(math.Float64frombits(binary.LittleEndian.Uint64(p.take(8)))))
	default:
		// A shared or array formula's reference, which ExpOf reads; a
		// what-if table; a table's column by name.
		p.bad = true
	}
}

// attr reads a token that says something about the formula besides its
// value: most only help Excel evaluate it, one is SUM with one argument,
// and one holds the spaces and line breaks the formula was typed with.
func (p *parser) attr() {
	switch kind := p.u8(); kind {
	case 0x00, 0x01, 0x02, 0x08, 0x20, 0x80:
		p.take(2)
	case 0x04: // CHOOSE's jumps
		n := int(p.u16())
		p.take(2*n + 2)
	case 0x10:
		p.take(2)
		p.push("SUM(" + p.pop() + ")")
	case 0x40, 0x41:
		typ, n := p.u8(), int(p.u8())
		ch := " "
		if typ == 1 || typ == 3 || typ == 5 {
			ch = "\n"
		}
		switch typ {
		case 2, 3:
			p.open += strings.Repeat(ch, n)
		case 4, 5:
			p.close += strings.Repeat(ch, n)
		default:
			p.space += strings.Repeat(ch, n)
		}
	default:
		p.bad = true
	}
}

func (p *parser) classified(t byte) {
	switch t & 0x1F {
	case 0x00:
		p.array()
	case 0x01: // a function with a fixed number of arguments
		id := p.u16()
		f, ok := functions[id]
		if !ok || f.min != f.max {
			p.bad = true
			return
		}
		p.call(f.name, int(f.min))
	case 0x02: // a function with a number of arguments
		n := int(p.u8() & 0x7F)
		id := p.u16()
		if id&0x7FFF == 0xFF {
			// A function named by its first argument: a macro's, an
			// add-in's, or one newer than this list.
			args := p.pops(n)
			if len(args) == 0 {
				p.bad = true
				return
			}
			space, open, close := p.space, p.open, p.close
			p.space, p.open, p.close = "", "", ""
			p.push(space + args[0] + open + "(" + strings.Join(args[1:], ",") + close + ")")
			return
		}
		name := ""
		if id&0x8000 != 0 {
			name = commands[id&0x7FFF]
		} else {
			name = functions[id].name
		}
		if name == "" {
			p.bad = true
			return
		}
		p.call(name, n)
	case 0x03: // a defined name
		p.push(p.name(int64(p.u32())))
	case 0x04:
		row, col := p.loc()
		p.push(p.cell(row, col, false))
	case 0x05:
		r0, r1, c0, c1 := p.area()
		p.push(p.rangeText(r0, r1, c0, c1, false))
	case 0x06: // what an area refers to, worked out ahead (PtgMemArea)
		p.take(6)
		n := int(int32(p.xu32()))
		p.takeExtra(16 * n)
	case 0x07, 0x08:
		p.take(6)
	case 0x09, 0x0E, 0x0F:
		p.take(2)
	case 0x0A:
		p.take(6)
		p.push("#REF!")
	case 0x0B:
		p.take(12)
		p.push("#REF!")
	case 0x0C:
		row, col := p.loc()
		p.push(p.cell(row, col, true))
	case 0x0D:
		r0, r1, c0, c1 := p.area()
		p.push(p.rangeText(r0, r1, c0, c1, true))
	case 0x19: // a name of another book, or of this one through its links
		_, l := p.link(int(p.u16()))
		i := int64(p.u32())
		if l == nil {
			return
		}
		if l.Kind == LinkSelf {
			p.push(p.name(i))
			return
		}
		if l.Kind == LinkOther || i < 1 || i > int64(len(l.Names)) || l.Names[i-1].Name == "" {
			p.bad = true
			return
		}
		n := l.Names[i-1]
		switch {
		case l.Kind != LinkBook:
			// A name the sheet uses, or an add-in's function: as it is.
			p.push(n.Name)
		case n.Sheet == 0:
			p.push(bookPrefix(l, nil) + "!" + n.Name)
		case n.Sheet > 0 && int(n.Sheet) <= len(l.Sheets):
			p.push(bookPrefix(l, l.Sheets[n.Sheet-1:n.Sheet]) + "!" + n.Name)
		default:
			p.bad = true
		}
	case 0x1A:
		sheets := p.sheets()
		row, col := p.loc()
		p.push(sheets + p.cell(row, col, false))
	case 0x1B:
		sheets := p.sheets()
		r0, r1, c0, c1 := p.area()
		p.push(sheets + p.rangeText(r0, r1, c0, c1, false))
	case 0x1C:
		sheets := p.sheets()
		p.take(6)
		p.push(sheets + "#REF!")
	case 0x1D:
		sheets := p.sheets()
		p.take(12)
		p.push(sheets + "#REF!")
	default:
		p.bad = true
	}
}

func (p *parser) call(name string, n int) {
	space, open, close := p.space, p.open, p.close
	p.space, p.open, p.close = "", "", ""
	args := p.pops(n)
	p.push(space + name + open + "(" + strings.Join(args, ",") + close + ")")
}

func (p *parser) name(i int64) string {
	if i < 1 || i > int64(len(p.book.Names)) || p.book.Names[i-1] == "" {
		p.bad = true
		return ""
	}
	return p.book.Names[i-1]
}

// loc reads a cell's place (RgceLoc): its row, and its column with two
// flags saying whether each is relative.
type ref struct {
	n        int32
	relative bool
}

func (p *parser) loc() (row, col ref) {
	r := int32(p.u32())
	c := p.u16()
	return ref{r & 0xFFFFF, c&0x8000 != 0}, ref{int32(c & 0x3FFF), c&0x4000 != 0}
}

func (p *parser) area() (r0, r1, c0, c1 ref) {
	a, b := int32(p.u32()), int32(p.u32())
	c, d := p.u16(), p.u16()
	return ref{a & 0xFFFFF, c&0x8000 != 0}, ref{b & 0xFFFFF, d&0x8000 != 0},
		ref{int32(c & 0x3FFF), c&0x4000 != 0}, ref{int32(d & 0x3FFF), d&0x4000 != 0}
}

// resolve turns a reference of a shared formula or a name, whose relative
// parts are offsets from where it is used, into a place on the sheet.
func (p *parser) resolve(row, col ref) (ref, ref) {
	if row.relative {
		off := row.n
		if off > 0x7FFFF {
			off -= 0x100000
		}
		row.n = wrap(p.at.Row+off, 1<<20)
	}
	if col.relative {
		off := col.n
		if off > 0x1FFF {
			off -= 0x4000
		}
		col.n = wrap(p.at.Col+off, 1<<14)
	}
	return row, col
}

func wrap(v, n int32) int32 {
	v %= n
	if v < 0 {
		v += n
	}
	return v
}

func (p *parser) cell(row, col ref, offset bool) string {
	if offset {
		row, col = p.resolve(row, col)
	}
	var b []byte
	b = appendCol(b, col)
	b = appendRow(b, row)
	return string(b)
}

func (p *parser) rangeText(r0, r1, c0, c1 ref, offset bool) string {
	if offset {
		r0, c0 = p.resolve(r0, c0)
		r1, c1 = p.resolve(r1, c1)
	}
	var b []byte
	switch {
	case r0.n == 0 && r1.n == 0xFFFFF:
		// Whole columns.
		b = appendCol(b, c0)
		b = append(b, ':')
		b = appendCol(b, c1)
	case c0.n == 0 && c1.n == 0x3FFF:
		// Whole rows.
		b = appendRow(b, r0)
		b = append(b, ':')
		b = appendRow(b, r1)
	default:
		b = appendCol(b, c0)
		b = appendRow(b, r0)
		b = append(b, ':')
		b = appendCol(b, c1)
		b = appendRow(b, r1)
	}
	return string(b)
}

func appendCol(b []byte, c ref) []byte {
	if !c.relative {
		b = append(b, '$')
	}
	var letters [3]byte
	n := 0
	for v := c.n + 1; v > 0 && n < 3; v = (v - 1) / 26 {
		letters[n] = byte('A' + (v-1)%26)
		n++
	}
	for n > 0 {
		n--
		b = append(b, letters[n])
	}
	return b
}

func appendRow(b []byte, r ref) []byte {
	if !r.relative {
		b = append(b, '$')
	}
	return strconv.AppendInt(b, int64(r.n)+1, 10)
}

// sheets reads the sheets a 3-D reference is to, written as its prefix:
// "Sheet1!", "'My sheet'!", "Sheet1:Sheet3!".
func (p *parser) sheets() string {
	x, l := p.link(int(p.u16()))
	if l == nil {
		return ""
	}
	switch l.Kind {
	case LinkSame:
		return ""
	case LinkSelf:
		n := int32(len(p.book.Sheets))
		if x.First < 0 || x.First >= n || x.Last < 0 || x.Last >= n {
			return "#REF!" // a sheet that has been deleted
		}
		return quote("", sheetSpan(p.book.Sheets, x)) + "!"
	case LinkBook:
		// Another workbook's sheets, by the number of the link: [1]Data!,
		// '[1]My sheet'!, [1]Jan:Mar!.
		n := int32(len(l.Sheets))
		if x.First < 0 || x.First >= n || x.Last < x.First || x.Last >= n {
			p.bad = true // a sheet it cannot name
			return ""
		}
		return bookPrefix(l, sheetSpan(l.Sheets, x)) + "!"
	}
	p.bad = true
	return ""
}

// sheetSpan is the sheets an entry of the table of sheet references is
// to: one, or the first and the last of a range of them.
func sheetSpan(sheets []string, x XTI) []string {
	if x.Last != x.First {
		return []string{sheets[x.First], sheets[x.Last]}
	}
	return []string{sheets[x.First]}
}

// quote writes the names of sheets after a prefix, joined by a colon, in
// quotes if any of them needs them: Data, 'My sheet', Jan:Mar,
// 'Jan:My sheet'.
func quote(prefix string, names []string) string {
	q := false
	for _, n := range names {
		q = q || quoted(n)
	}
	s := prefix + strings.Join(names, ":")
	if q {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

// link reads the reference to the table of sheet references that a 3-D
// reference or a name through the links starts with: the entry, and the
// link it is through. The link is nil if there is none.
func (p *parser) link(i int) (XTI, *Link) {
	if i >= len(p.book.XTI) {
		p.bad = true
		return XTI{}, nil
	}
	x := p.book.XTI[i]
	if x.Link < 0 || int(x.Link) >= len(p.book.Links) {
		p.bad = true
		return XTI{}, nil
	}
	return x, &p.book.Links[x.Link]
}

// bookPrefix writes another workbook's number before the name of one of
// its sheets, or alone: [1]Data, '[1]My sheet', [1].
func bookPrefix(l *Link, sheets []string) string {
	s := "[" + strconv.Itoa(l.Book) + "]"
	if len(sheets) == 0 {
		return s
	}
	return quote(s, sheets)
}

// quoted reports whether a sheet's name needs quotes in a formula: one
// that is not a word, or that could be read as something else: a cell
// ("A1"), a cell in R1C1 form ("R1C1", "R", "C"), a number or a truth
// value.
func quoted(name string) bool {
	if name == "" {
		return true
	}
	for i, r := range name {
		switch {
		case r == '_' || r > 0x7F:
		case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' || r == '.':
			if i == 0 {
				return true
			}
		default:
			return true
		}
	}
	upper := strings.ToUpper(name)
	if upper == "TRUE" || upper == "FALSE" {
		return true
	}
	// Letters then digits: a cell if the letters are a column (at most
	// XFD) and the digits a row.
	digits := strings.TrimLeft(upper, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	letters := upper[:len(upper)-len(digits)]
	if len(letters) >= 1 && len(letters) <= 3 && digits != "" && strings.Trim(digits, "0123456789") == "" {
		if len(letters) < 3 || letters <= "XFD" {
			return true
		}
	}
	// R1C1: R or C, alone or with a number, or both.
	rest := upper
	if strings.HasPrefix(rest, "R") {
		rest = strings.TrimLeft(rest[1:], "0123456789")
	}
	if strings.HasPrefix(rest, "C") {
		rest = strings.TrimLeft(rest[1:], "0123456789")
	}
	return rest == "" && upper != ""
}

// The rows a reference to a table is to (PtgRowType), as its text names
// them; the data rows alone are named by naming none.
var tableRows = map[uint16][]string{
	0x00: nil,
	0x01: {"#All"},
	0x02: {"#Headers"},
	0x04: {"#Data"},
	0x06: {"#Headers", "#Data"},
	0x08: {"#Totals"},
	0x0C: {"#Data", "#Totals"},
	0x10: {"#This Row"},
}

// list reads a reference to a table's rows and columns by their names
// (PtgList): Sales[Amount], Sales[[#Headers],[Amount]], Sales[#All],
// Sales[[Q1]:[Q4]]. A table of another workbook is named in the extra
// bytes, with its columns: [1]!Sales[Amount].
func (p *parser) list() {
	ixti := int(p.u16())
	flags := p.u16()
	id := p.u32()
	c0, c1 := int(p.u16()), int(p.u16())
	if p.bad {
		return
	}
	if flags&0x1000 != 0 { // invalid: the table or its columns are gone
		p.push("#REF!")
		return
	}
	var name string
	var cols []string
	rowType := flags >> 2 & 0x1F
	if flags&0x2000 != 0 {
		// Another workbook's table (PtgExtraList).
		_, l := p.link(ixti)
		has := p.takeExtra(1)[0]
		rowType = binary.LittleEndian.Uint16(p.takeExtra(2)) & 0x1F
		name = p.extraString()
		if has != 0 {
			p.takeExtra(2)
			n := int(p.takeExtra(1)[0])
			if n != 1 && n != 2 {
				p.bad = true
				return
			}
			for range n {
				p.takeExtra(3) // whether another follows; reserved
				cols = append(cols, p.extraString())
			}
		}
		if l == nil || l.Kind != LinkBook || name == "" {
			p.bad = true
			return
		}
		name = bookPrefix(l, nil) + "!" + name
	} else {
		var t Table
		ok := false
		if p.book.Tables != nil {
			t, ok = p.book.Tables()[id]
		}
		if !ok || t.Name == "" {
			p.bad = true
			return
		}
		name = t.Name
		switch flags & 3 {
		case 0:
		case 1:
			c1 = c0
			fallthrough
		case 2:
			if c0 > c1 || c1 >= len(t.Columns) {
				p.bad = true
				return
			}
			cols = []string{t.Columns[c0]}
			if c1 != c0 {
				cols = append(cols, t.Columns[c1])
			}
		default:
			p.bad = true
			return
		}
	}
	rows, ok := tableRows[rowType]
	if !ok || p.bad {
		p.bad = true
		return
	}
	for _, c := range cols {
		if c == "" {
			p.bad = true
			return
		}
	}
	p.push(name + tableSpec(rows, cols, flags&0x80 != 0, flags&0x100 != 0))
}

// extraString reads a string of the extra bytes: a 16-bit count of UTF-16
// code units, then the units.
func (p *parser) extraString() string {
	n := int(binary.LittleEndian.Uint16(p.takeExtra(2)))
	return string(AppendUTF16(nil, p.takeExtra(2*n)))
}

// tableSpec writes what follows a table's name in a reference to it: the
// rows, by their special names, and the columns, one or a range. One
// thing alone needs one pair of brackets, unless it is a column whose name
// has a character that means something in a formula.
func tableSpec(rows, cols []string, bracketSpace, commaSpace bool) string {
	var items []string
	for _, r := range rows {
		items = append(items, "["+r+"]")
	}
	switch len(cols) {
	case 1:
		if len(items) == 0 {
			c := escapeColumn(cols[0])
			if strings.ContainsAny(cols[0], "\t\n\r,:.[]#'\"{}$^&*+=->/</@\\!()%?`;~_") {
				return "[[" + c + "]]"
			}
			return "[" + c + "]"
		}
		items = append(items, "["+escapeColumn(cols[0])+"]")
	case 2:
		items = append(items, "["+escapeColumn(cols[0])+"]:["+escapeColumn(cols[1])+"]")
	}
	if len(items) == 0 {
		return "[]"
	}
	if len(items) == 1 && len(rows) == 1 {
		return items[0]
	}
	sep := ","
	if commaSpace {
		sep = ", "
	}
	inner := strings.Join(items, sep)
	if bracketSpace {
		return "[ " + inner + " ]"
	}
	return "[" + inner + "]"
}

// escapeColumn writes a column's name as a reference holds it, with a
// single quote before each bracket, number sign and single quote.
func escapeColumn(name string) string {
	if !strings.ContainsAny(name, "[]#'") {
		return name
	}
	var b strings.Builder
	for _, r := range name {
		if r == '[' || r == ']' || r == '#' || r == '\'' {
			b.WriteByte('\'')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// array reads an array of constants ({1,2;3,4}), whose values are in the
// extra bytes.
func (p *parser) array() {
	p.take(14)
	rows, cols := int(int32(p.xu32())), int(int32(p.xu32()))
	if rows <= 0 || cols <= 0 || rows*cols > 1<<16 {
		p.bad = true
		return
	}
	var b strings.Builder
	b.WriteByte('{')
	for r := 0; r < rows && !p.bad; r++ {
		if r > 0 {
			b.WriteByte(';')
		}
		for c := 0; c < cols && !p.bad; c++ {
			if c > 0 {
				b.WriteByte(',')
			}
			switch p.takeExtra(1)[0] {
			case 0:
				b.WriteString(number(math.Float64frombits(binary.LittleEndian.Uint64(p.takeExtra(8)))))
			case 1:
				n := int(binary.LittleEndian.Uint16(p.takeExtra(2)))
				s := string(AppendUTF16(nil, p.takeExtra(2*n)))
				b.WriteString(`"` + strings.ReplaceAll(s, `"`, `""`) + `"`)
			case 2:
				if p.takeExtra(1)[0] != 0 {
					b.WriteString("TRUE")
				} else {
					b.WriteString("FALSE")
				}
			case 4:
				b.WriteString(ErrorText(p.takeExtra(1)[0]))
				p.takeExtra(3)
			default:
				p.bad = true
			}
			if b.Len() > maxFormula {
				p.bad = true
			}
		}
	}
	b.WriteByte('}')
	p.push(b.String())
}

// number writes a number of a formula as Excel does: as few digits as
// read back as it, written out in full unless it is very large or very
// small, and then with E and no leading zeros in the exponent.
func number(v float64) string {
	if a := math.Abs(v); a == 0 || a >= 1e-9 && a < 1e15 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	s := strconv.FormatFloat(v, 'E', -1, 64)
	i := strings.IndexByte(s, 'E')
	exp := strings.TrimLeft(s[i+2:], "0")
	return s[:i+2] + exp
}
