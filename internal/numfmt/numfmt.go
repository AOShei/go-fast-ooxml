// Package numfmt renders a spreadsheet cell's value the way the cell shows
// it: through its number format.
//
// A number format is a small language. "#,##0.00" writes 1234.5 as
// "1,234.50"; "0.0%" writes 0.125 as "12.5%"; up to four sections separated
// by semicolons give different renderings to positive numbers, negative
// numbers, zero and text. This package parses that language and applies it.
//
// Two things are deliberately not as displayed:
//
//   - Dates and times are written in ISO 8601 whatever the format, because
//     "03/04/2026" names two different days depending on where the file was
//     made. Nothing the value holds is dropped: a value with a time of day
//     keeps it.
//   - Padding is not reproduced. A format can ask for blank space the width
//     of a digit or of some character, and for a character repeated to fill
//     the cell. Such space is written as one ordinary space, and space at
//     the ends is removed.
//
// The behaviour was fixed against what LibreOffice displays for a table of
// formats and values (testdata/numfmt), and follows it where spreadsheet
// programs differ.
package numfmt

import (
	"math"
	"strconv"
	"strings"
)

// maxCodeLength bounds a format code. Excel's own limit is 255 characters.
// A code is applied to every cell that uses it, so its length bounds how
// much a format can add to each.
const maxCodeLength = 512

// Format is a parsed number format. It is safe for concurrent use.
type Format struct {
	sections []section // for numbers: one to three
	text     *section  // for text: the fourth section, or the only one if it shows text
	conds    bool      // some section has a condition written in the code
}

type kind uint8

const (
	kindNumber  kind = iota // digit placeholders, or literal text only
	kindGeneral             // the General format, possibly with text around it
	kindDate                // a date or a time of day
	kindElapsed             // a duration: [h]:mm:ss
	kindText                // shows the cell's text, or a number as General
)

type section struct {
	kind kind
	cond cond
	toks []token

	// For kindNumber:
	intSlots  int  // digit placeholders before the decimal point
	fracSlots int  // and after it
	thousands bool // digits are grouped in threes
	shift     int  // decimal places the value is shifted by percent signs and scaling commas
	exponent  bool // scientific notation
	expSlots  int
	fraction  bool // a vulgar fraction
	numSlots  int  // placeholders of the numerator
	denSlots  int  // placeholders of the denominator
	fixedDen  int  // or the denominator itself

	// For kindDate and kindElapsed:
	hasDate   bool // the format names a year, month or day
	secDigits int  // decimals of the seconds
	hasSecs   bool
}

type cond struct {
	op    uint8 // 0 none, '<', '>', '=', 'l' <=, 'g' >=, 'n' <>
	value float64
}

func (c cond) match(v float64) bool {
	switch c.op {
	case '<':
		return v < c.value
	case '>':
		return v > c.value
	case '=':
		return v == c.value
	case 'l':
		return v <= c.value
	case 'g':
		return v >= c.value
	case 'n':
		return v != c.value
	}
	return true
}

type tokKind uint8

const (
	tokLiteral   tokKind = iota // text written as it is
	tokPad                      // blank space the format asks for: one space
	tokDigit                    // a digit placeholder of the integer part: 0, # or ?
	tokPoint                    // the decimal point
	tokFracDigit                // a digit placeholder after the point
	tokExp                      // E+ or E-, followed by tokExpDigit
	tokExpDigit
	tokNumDigit // a placeholder of a fraction's numerator
	tokSlash
	tokDenDigit // a placeholder of a fraction's denominator
	tokFixedDen // a fraction's given denominator
	tokGeneral
	tokText    // @
	tokElapsed // [h], [mm], [s]: a total, of hours, minutes or seconds
	tokHour    // h, hh
	tokMinute  // m, mm in a time
	tokSecond  // s, ss
	tokSecFrac // .00 after seconds
	tokDate    // any other part of a date; dates are not rendered from tokens
)

type token struct {
	kind tokKind
	ch   byte   // the placeholder character; the unit of tokElapsed; '+' for an exponent that always has a sign
	n    int    // a count: minimum digits, a fixed denominator
	text string // of a literal
}

// general is what a cell with no format of its own uses.
var general = &Format{sections: []section{{kind: kindGeneral, toks: []token{{kind: tokGeneral}}}}}

// General returns the General format.
func General() *Format { return general }

// Parse reads a format code. It does not fail: a code that cannot be
// understood gives the General format, which shows the value in full.
func Parse(code string) *Format {
	if code == "" || len(code) > maxCodeLength || strings.EqualFold(code, "General") {
		return general
	}
	parts := splitSections(code)
	if len(parts) > 4 {
		parts = parts[:4]
	}
	f := &Format{}
	for i, p := range parts {
		s, ok := parseSection(p)
		if !ok {
			return general
		}
		if s.cond.op != 0 {
			f.conds = true
		}
		if i == 3 {
			f.text = &s
			break
		}
		f.sections = append(f.sections, s)
	}
	// A format of one section that shows text is the text format.
	if len(parts) == 1 && f.sections[0].kind == kindText {
		f.text = &f.sections[0]
	}
	return f
}

// splitSections cuts a code at the semicolons that separate sections,
// leaving alone those inside quotes, brackets and escapes.
func splitSections(code string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '"':
			for i++; i < len(code) && code[i] != '"'; i++ {
			}
		case '[':
			for i++; i < len(code) && code[i] != ']'; i++ {
			}
		case '\\', '_', '*':
			i++
		case ';':
			parts = append(parts, code[start:i])
			start = i + 1
		}
	}
	return append(parts, code[start:])
}

// parseSection reads one section of a format code.
func parseSection(code string) (section, bool) {
	var s section
	var toks []token
	literal := func(text string) {
		if n := len(toks); n > 0 && toks[n-1].kind == tokLiteral && toks[n-1].ch == 0 {
			toks[n-1].text += text
			return
		}
		toks = append(toks, token{kind: tokLiteral, text: text})
	}
	var dateLetters, digits, elapsed bool
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(code) && code[j] != '"' {
				j++
			}
			literal(code[i+1 : j])
			i = j
		case c == '\\':
			if i+1 < len(code) {
				_, size := decodeRune(code[i+1:])
				literal(code[i+1 : i+1+size])
				i += size
			}
		case c == '_':
			// Space as wide as the next character.
			if i+1 < len(code) {
				_, size := decodeRune(code[i+1:])
				i += size
			}
			toks = append(toks, token{kind: tokPad})
		case c == '*':
			// The next character repeated to fill the cell: layout only.
			if i+1 < len(code) {
				_, size := decodeRune(code[i+1:])
				i += size
			}
		case c == '[':
			j := strings.IndexByte(code[i:], ']')
			if j < 0 {
				return s, false
			}
			inner := code[i+1 : i+j]
			i += j
			switch {
			case inner == "":
			case inner[0] == '$':
				// A currency symbol with a locale: [$€-407].
				symbol, _, _ := strings.Cut(inner[1:], "-")
				if symbol != "" {
					literal(symbol)
				}
			case inner[0] == '<' || inner[0] == '>' || inner[0] == '=':
				c, ok := parseCond(inner)
				if !ok {
					return s, false
				}
				s.cond = c
			case isRun(inner, 'h') || isRun(inner, 'm') || isRun(inner, 's'):
				elapsed = true
				toks = append(toks, token{kind: tokElapsed, ch: inner[0] | 0x20, n: len(inner)})
			default:
				// A colour, or a calendar or digit-shape instruction.
			}
		case c == '0' || c == '#' || c == '?':
			digits = true
			toks = append(toks, token{kind: tokDigit, ch: c})
		case c == '.':
			toks = append(toks, token{kind: tokPoint})
		case c == ',':
			toks = append(toks, token{kind: tokLiteral, text: ",", ch: ','})
		case c == '%':
			s.shift += 2
			literal("%")
		case (c == 'E' || c == 'e') && i+1 < len(code) && (code[i+1] == '+' || code[i+1] == '-'):
			toks = append(toks, token{kind: tokExp, ch: code[i+1]})
			i++
		case c == '/':
			// A fraction's bar; after it, placeholders or the denominator
			// itself, as in "# ?/16".
			j := i + 1
			den := 0
			for j < len(code) && code[j] >= '0' && code[j] <= '9' && j-i < 9 {
				den = den*10 + int(code[j]-'0')
				j++
			}
			if den > 0 && code[i+1] != '0' {
				toks = append(toks, token{kind: tokSlash}, token{kind: tokFixedDen, n: den})
				i = j - 1
			} else {
				toks = append(toks, token{kind: tokSlash})
			}
		case c == '@':
			toks = append(toks, token{kind: tokText})
		case (c == 'G' || c == 'g') && len(code)-i >= 7 && strings.EqualFold(code[i:i+7], "General"):
			toks = append(toks, token{kind: tokGeneral})
			i += 6
		case (c == 'A' || c == 'a') && len(code)-i >= 5 && strings.EqualFold(code[i:i+5], "AM/PM"):
			dateLetters = true
			toks = append(toks, token{kind: tokDate})
			i += 4
		case (c == 'A' || c == 'a') && len(code)-i >= 3 && strings.EqualFold(code[i:i+3], "A/P"):
			dateLetters = true
			toks = append(toks, token{kind: tokDate})
			i += 2
		case isDateLetter(c):
			j := i
			for j < len(code) && code[j]|0x20 == c|0x20 {
				j++
			}
			dateLetters = true
			t := token{kind: tokDate, ch: c | 0x20, n: j - i}
			switch c | 0x20 {
			case 'h':
				t.kind = tokHour
			case 's':
				t.kind = tokSecond
				s.hasSecs = true
			case 'm':
				t.kind = tokMinute // month or minute; it matters only for durations
			case 'y', 'd':
				s.hasDate = true
			}
			toks = append(toks, t)
			i = j - 1
		default:
			_, size := decodeRune(code[i:])
			literal(code[i : i+size])
			i += size - 1
		}
	}
	s.toks = toks
	switch {
	case elapsed || dateLetters:
		s.kind = kindDate
		if elapsed {
			s.kind = kindElapsed
		}
		s.timeLayout()
	case has(toks, tokGeneral):
		s.kind = kindGeneral
	case has(toks, tokText):
		s.kind = kindText
	default:
		s.kind = kindNumber
		if digits {
			s.numberLayout()
		}
	}
	return s, true
}

func has(toks []token, k tokKind) bool {
	for i := range toks {
		if toks[i].kind == k {
			return true
		}
	}
	return false
}

func isRun(s string, letter byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i]|0x20 != letter {
			return false
		}
	}
	return len(s) > 0
}

func isDateLetter(c byte) bool {
	switch c | 0x20 {
	case 'y', 'm', 'd', 'h', 's':
		return true
	}
	return false
}

// decodeRune returns the length of the first character of s, without
// importing a table: only the length is needed.
func decodeRune(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	c := s[0]
	size := 1
	switch {
	case c >= 0xF0:
		size = 4
	case c >= 0xE0:
		size = 3
	case c >= 0xC0:
		size = 2
	}
	if size > len(s) {
		size = len(s)
	}
	return rune(c), size
}

func parseCond(s string) (cond, bool) {
	var c cond
	switch {
	case strings.HasPrefix(s, "<="):
		c.op, s = 'l', s[2:]
	case strings.HasPrefix(s, ">="):
		c.op, s = 'g', s[2:]
	case strings.HasPrefix(s, "<>"):
		c.op, s = 'n', s[2:]
	case s[0] == '<', s[0] == '>', s[0] == '=':
		c.op, s = s[0], s[1:]
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return c, false
	}
	c.value = v
	return c, true
}

// timeLayout settles the tokens of a date or duration section: digit
// placeholders after a decimal point that follows the seconds are the
// decimals of the seconds; other number tokens mean nothing here.
func (s *section) timeLayout() {
	toks := s.toks[:0]
	for i := 0; i < len(s.toks); i++ {
		t := s.toks[i]
		switch t.kind {
		case tokPoint:
			n := 0
			for i+1 < len(s.toks) && s.toks[i+1].kind == tokDigit {
				n++
				i++
			}
			if n > 0 {
				s.secDigits = min(n, 3)
				toks = append(toks, token{kind: tokSecFrac, n: s.secDigits})
			} else {
				toks = append(toks, token{kind: tokLiteral, text: "."})
			}
		case tokDigit, tokExp, tokText, tokGeneral, tokFixedDen:
			// not meaningful in a date
		case tokSlash:
			toks = append(toks, token{kind: tokLiteral, text: "/"})
		case tokElapsed:
			if t.ch == 's' {
				s.hasSecs = true
			}
			toks = append(toks, t)
		default:
			toks = append(toks, t)
		}
	}
	s.toks = toks
}

// numberLayout works out the structure of a section with digit
// placeholders: which are the integer part, the decimals, an exponent or a
// fraction; which commas group thousands and which scale the value.
func (s *section) numberLayout() {
	toks := s.toks
	// A fraction: placeholders, a bar, then placeholders or a denominator,
	// with no decimal point.
	slash := -1
	for i := range toks {
		if toks[i].kind == tokSlash {
			slash = i
			break
		}
	}
	if slash > 0 && !has(toks, tokPoint) && !has(toks, tokExp) && toks[slash-1].kind == tokDigit &&
		slash+1 < len(toks) && (toks[slash+1].kind == tokDigit || toks[slash+1].kind == tokFixedDen) {
		s.fraction = true
		for i := slash - 1; i >= 0 && toks[i].kind == tokDigit; i-- {
			toks[i].kind = tokNumDigit
			s.numSlots++
		}
		if toks[slash+1].kind == tokFixedDen {
			s.fixedDen = toks[slash+1].n
		} else {
			for i := slash + 1; i < len(toks) && toks[i].kind == tokDigit; i++ {
				toks[i].kind = tokDenDigit
				s.denSlots++
			}
		}
	}
	// Any other bar is a character.
	for i := range toks {
		if toks[i].kind == tokSlash && !s.fraction || toks[i].kind == tokFixedDen && !s.fraction {
			toks[i] = token{kind: tokLiteral, text: "/" + fixedText(toks[i])}
		}
	}
	// The first point is the decimal point; placeholders after it are
	// decimals, up to an exponent.
	seenPoint, seenExp := false, false
	lastDigit := -1
	for i := range toks {
		switch toks[i].kind {
		case tokPoint:
			if seenPoint || seenExp || s.fraction {
				toks[i] = token{kind: tokLiteral, text: "."}
			}
			seenPoint = true
		case tokExp:
			if seenExp {
				toks[i] = token{kind: tokLiteral, text: "E" + string(toks[i].ch)}
			} else {
				seenExp, s.exponent = true, true
			}
		case tokDigit:
			lastDigit = i
			switch {
			case seenExp:
				toks[i].kind = tokExpDigit
				s.expSlots++
			case seenPoint:
				toks[i].kind = tokFracDigit
				s.fracSlots++
			default:
				s.intSlots++
			}
		case tokNumDigit, tokDenDigit:
			lastDigit = i
		}
	}
	if s.exponent && s.expSlots == 0 {
		s.exponent = false
	}
	// Commas. Between placeholders of the integer part they group
	// thousands; straight after the last placeholder each divides the
	// value by a thousand; anywhere else they are characters.
	out := toks[:0]
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind != tokLiteral || t.ch != ',' {
			out = append(out, t)
			continue
		}
		before := i > 0 && isNumberSlot(toks[i-1].kind)
		switch {
		case before && i < lastDigit && nextIsSlot(toks, i):
			s.thousands = true
		case i > lastDigit && lastDigit >= 0 && onlyCommasBetween(toks, lastDigit, i):
			s.shift -= 3
		default:
			out = append(out, token{kind: tokLiteral, text: ","})
		}
	}
	// Merge the literals the passes above left side by side.
	merged := out[:0]
	for _, t := range out {
		if n := len(merged); n > 0 && t.kind == tokLiteral && merged[n-1].kind == tokLiteral {
			merged[n-1].text += t.text
			merged[n-1].ch = 0
			continue
		}
		if t.kind == tokLiteral {
			t.ch = 0
		}
		merged = append(merged, t)
	}
	s.toks = merged
}

func fixedText(t token) string {
	if t.kind == tokFixedDen {
		return strconv.Itoa(t.n)
	}
	return ""
}

func isNumberSlot(k tokKind) bool {
	return k == tokDigit || k == tokFracDigit || k == tokExpDigit || k == tokNumDigit || k == tokDenDigit
}

func nextIsSlot(toks []token, i int) bool {
	for j := i + 1; j < len(toks); j++ {
		if toks[j].kind == tokLiteral && toks[j].ch == ',' {
			continue
		}
		return toks[j].kind == tokDigit
	}
	return false
}

func onlyCommasBetween(toks []token, from, to int) bool {
	for j := from + 1; j < to; j++ {
		if toks[j].kind != tokLiteral || toks[j].ch != ',' {
			return false
		}
	}
	return true
}

// Result says what Number or Text made of a value.
type Result uint8

const (
	// Shown: the value is displayed.
	Shown Result = iota
	// Blank: the format displays nothing for this value. The value is
	// written in full instead, so that it is not lost; the caller can
	// report that the cell hides it.
	Blank
	// Date: the value is a date or a time, written in ISO 8601.
	Date
)

// Number appends v as the format displays it. date1904 says the workbook
// counts days from 1904.
func (f *Format) Number(dst []byte, v float64, date1904 bool) ([]byte, Result) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return AppendGeneral(dst, 0), Shown
	}
	s, minus := f.pick(v)
	if s == nil {
		return AppendGeneral(dst, v), Shown
	}
	start := len(dst)
	written := false
	switch s.kind {
	case kindGeneral, kindText:
		for _, t := range s.toks {
			switch t.kind {
			case tokLiteral:
				dst = append(dst, t.text...)
			case tokPad:
				dst = append(dst, ' ')
			case tokGeneral, tokText:
				// Once: a format that repeats the value would let a
				// small file ask for a large output.
				if written {
					continue
				}
				written = true
				if minus {
					dst = AppendGeneral(dst, v)
				} else {
					dst = AppendGeneral(dst, math.Abs(v))
				}
			}
		}
	case kindDate:
		if out, ok := appendISO(dst, v, date1904, s.hasDate); ok {
			return out, Date
		}
		// Not a date the calendar has: the number itself.
		return AppendGeneral(dst, v), Shown
	case kindElapsed:
		dst = s.appendElapsed(dst, v)
	default:
		dst = s.appendNumber(dst, v, minus)
	}
	dst = tidy(dst, start)
	if len(dst) == start {
		// Nothing is displayed. Zero shown as nothing is an everyday
		// choice and is left so; any other value is written out.
		if v == 0 {
			return dst, Shown
		}
		return AppendGeneral(dst, v), Blank
	}
	return dst, Shown
}

// Text appends a cell's text as the format displays it.
func (f *Format) Text(dst []byte, text string) ([]byte, Result) {
	if f.text == nil {
		return append(dst, text...), Shown
	}
	start := len(dst)
	shows := false
	for _, t := range f.text.toks {
		switch t.kind {
		case tokLiteral:
			dst = append(dst, t.text...)
		case tokPad:
			dst = append(dst, ' ')
		case tokText, tokGeneral:
			if !shows { // once, as for numbers
				dst = append(dst, text...)
			}
			shows = true
		}
	}
	if !shows && strings.TrimSpace(text) != "" {
		// The format replaces the text with its own, or with nothing.
		dst = append(dst[:start], text...)
		return dst, Blank
	}
	return dst, Shown
}

// PlainText reports whether the format shows a cell's text as it is,
// which nearly every format does.
func (f *Format) PlainText() bool {
	return f.text == nil || len(f.text.toks) == 1 && f.text.toks[0].kind == tokText
}

// IsDate reports whether the format shows numbers as dates or times.
func (f *Format) IsDate() bool {
	return len(f.sections) > 0 && f.sections[0].kind == kindDate
}

// pick chooses the section for a number, and says whether a negative
// number gets a minus sign written for it.
func (f *Format) pick(v float64) (*section, bool) {
	n := len(f.sections)
	if n == 0 {
		return nil, true
	}
	s := f.sections
	if !f.conds {
		switch {
		case n == 1:
			return &s[0], true
		case v < 0:
			return &s[1], false
		case n == 3 && v == 0:
			return &s[2], true
		}
		return &s[0], true
	}
	// With conditions: the first section whose condition holds. A section
	// with none takes the usual one for its place, and the last takes
	// whatever is left.
	first := s[0].cond
	if first.op == 0 {
		first = cond{op: '>'}
		if n < 3 {
			first.op = 'g'
		}
	}
	if first.match(v) {
		return &s[0], true
	}
	if n == 1 {
		return nil, true
	}
	second := s[1].cond
	if second.op == 0 && n == 3 {
		second = cond{op: '<'}
	}
	if second.match(v) {
		return &s[1], false
	}
	if n == 3 {
		return &s[2], true
	}
	return nil, true
}

// tidy makes the padding of a rendering plain: runs of spaces become one,
// and space at the ends goes.
func tidy(dst []byte, start int) []byte {
	out := dst[:start]
	space := true // drops leading space
	for _, c := range dst[start:] {
		if c == ' ' {
			if !space {
				out = append(out, ' ')
			}
			space = true
			continue
		}
		space = false
		out = append(out, c)
	}
	if n := len(out); n > start && out[n-1] == ' ' {
		out = out[:n-1]
	}
	return out
}

// Builtin returns the code of a built-in format, by its number. The
// numbers above 49 that Excel reserves for East Asian dates are given as a
// date. Unknown numbers are General.
func Builtin(id int) string {
	if id >= 0 && id < len(builtins) && builtins[id] != "" {
		return builtins[id]
	}
	if id >= 27 && id <= 36 || id >= 50 && id <= 58 {
		return "yyyy-mm-dd"
	}
	return "General"
}

var builtins = [...]string{
	0:  "General",
	1:  "0",
	2:  "0.00",
	3:  "#,##0",
	4:  "#,##0.00",
	5:  `"$"#,##0_);("$"#,##0)`,
	6:  `"$"#,##0_);[Red]("$"#,##0)`,
	7:  `"$"#,##0.00_);("$"#,##0.00)`,
	8:  `"$"#,##0.00_);[Red]("$"#,##0.00)`,
	9:  "0%",
	10: "0.00%",
	11: "0.00E+00",
	12: "# ?/?",
	13: "# ??/??",
	14: "m/d/yyyy",
	15: "d-mmm-yy",
	16: "d-mmm",
	17: "mmm-yy",
	18: "h:mm AM/PM",
	19: "h:mm:ss AM/PM",
	20: "h:mm",
	21: "h:mm:ss",
	22: "m/d/yyyy h:mm",
	37: "#,##0_);(#,##0)",
	38: "#,##0_);[Red](#,##0)",
	39: "#,##0.00_);(#,##0.00)",
	40: "#,##0.00_);[Red](#,##0.00)",
	41: `_(* #,##0_);_(* \(#,##0\);_(* "-"_);_(@_)`,
	42: `_("$"* #,##0_);_("$"* \(#,##0\);_("$"* "-"_);_(@_)`,
	43: `_(* #,##0.00_);_(* \(#,##0.00\);_(* "-"??_);_(@_)`,
	44: `_("$"* #,##0.00_);_("$"* \(#,##0.00\);_("$"* "-"??_);_(@_)`,
	45: "mm:ss",
	46: "[h]:mm:ss",
	47: "mm:ss.0",
	48: "##0.0E+0",
	49: "@",
}
