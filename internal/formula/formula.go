// Package formula moves the references of a spreadsheet formula.
//
// Excel stores a formula that a range of cells share once, in the first
// cell of the range, as that cell's formula ("shared formulas"). The
// formula of another cell of the range is the same formula with its
// relative references moved by as many rows and columns as that cell is
// from the first: =A1*2 in B1 is =A2*2 in B2. Shift does that. It moves
// references in A1 notation (A1, $A$1, A1:B2, A:A, 1:1, with or without a
// sheet) and leaves everything else as it is: strings, names, functions,
// structured references, numbers. A reference moved off the sheet becomes
// #REF!, as in Excel.
package formula

import "strings"

// Limits of a sheet.
const (
	maxRow = 1 << 20 // 1,048,576
	maxCol = 1 << 14 // 16,384, column XFD
)

// Shift returns f with its relative references moved by rows and cols.
func Shift(f string, rows, cols int) string {
	if rows == 0 && cols == 0 {
		return f
	}
	var sb strings.Builder
	sb.Grow(len(f) + 8)
	i := 0
	for i < len(f) {
		c := f[i]
		switch {
		case c == '"':
			// A string, with "" for a quote inside it.
			j := i + 1
			for j < len(f) {
				if f[j] == '"' {
					if j+1 < len(f) && f[j+1] == '"' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			sb.WriteString(f[i:min(j, len(f))])
			i = j
		case c == '\'':
			// A quoted sheet name, with '' for a quote inside it.
			j := i + 1
			for j < len(f) {
				if f[j] == '\'' {
					if j+1 < len(f) && f[j+1] == '\'' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			sb.WriteString(f[i:min(j, len(f))])
			i = j
		case c == '[':
			// A structured reference or the number of another workbook,
			// with brackets inside it.
			depth, j := 0, i
			for j < len(f) {
				if f[j] == '[' {
					depth++
				} else if f[j] == ']' {
					depth--
					if depth == 0 {
						j++
						break
					}
				}
				j++
			}
			sb.WriteString(f[i:j])
			i = j
		case c == '#':
			// An error value: #REF!, #N/A, #DIV/0!.
			j := i + 1
			for j < len(f) && (isWord(f[j]) || f[j] == '/' || f[j] == '!' || f[j] == '?') {
				j++
			}
			sb.WriteString(f[i:j])
			i = j
		case startsToken(f, i) && (c == '$' || isLetter(c) || isDigit(c)):
			if n, out := reference(f, i, rows, cols); n > 0 {
				sb.WriteString(out)
				i += n
				continue
			}
			// Not a reference: a name, a function, a number, copied whole.
			j := i + 1
			for j < len(f) && (isWord(f[j]) || f[j] == '.' || f[j] == '\\' || f[j] == '$' || f[j] >= 0x80) {
				j++
			}
			sb.WriteString(f[i:j])
			i = j
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String()
}

// reference reads a reference at f[i:] (a cell, a range of cells, whole
// columns or whole rows) and returns how many bytes it takes and the
// moved reference, or 0 if there is none.
func reference(f string, i, rows, cols int) (int, string) {
	// A cell, and a range of two.
	if n, out := cell(f, i, rows, cols); n > 0 {
		if i+n < len(f) && f[i+n] == ':' {
			if m, out2 := cell(f, i+n+1, rows, cols); m > 0 && ends(f, i+n+1+m) {
				return n + 1 + m, join(out, out2)
			}
		}
		if ends(f, i+n) {
			return n, out
		}
		return 0, ""
	}
	// Whole columns, A:C, and whole rows, 1:3.
	if n, out, ok := column(f, i, cols); ok && i+n < len(f) && f[i+n] == ':' {
		if m, out2, ok := column(f, i+n+1, cols); ok && ends(f, i+n+1+m) {
			return n + 1 + m, join(out, out2)
		}
	}
	if n, out, ok := row(f, i, rows); ok && i+n < len(f) && f[i+n] == ':' {
		if m, out2, ok := row(f, i+n+1, rows); ok && ends(f, i+n+1+m) {
			return n + 1 + m, join(out, out2)
		}
	}
	return 0, ""
}

// join writes a range, or #REF! if either end has gone off the sheet.
func join(a, b string) string {
	if a == "#REF!" || b == "#REF!" {
		return "#REF!"
	}
	return a + ":" + b
}

// cell reads a cell reference, $A$1, at f[i:].
func cell(f string, i, rows, cols int) (int, string) {
	n, col, ok := column(f, i, cols)
	if !ok {
		return 0, ""
	}
	m, rw, ok := row(f, i+n, rows)
	if !ok {
		return 0, ""
	}
	if col == "#REF!" || rw == "#REF!" {
		return n + m, "#REF!"
	}
	return n + m, col + rw
}

// column reads a column, $AB, at f[i:].
func column(f string, i, cols int) (int, string, bool) {
	j := i
	fixed := j < len(f) && f[j] == '$'
	if fixed {
		j++
	}
	k := j
	v := 0
	for k < len(f) && k-j < 3 && isLetter(f[k]) {
		v = v*26 + int(upper(f[k])-'A'+1)
		k++
	}
	if k == j || v > maxCol || k < len(f) && isLetter(f[k]) {
		return 0, "", false
	}
	if fixed {
		return k - i, f[i:k], true
	}
	v += cols
	if v < 1 || v > maxCol {
		return k - i, "#REF!", true
	}
	return k - i, columnName(v), true
}

// row reads a row, $12, at f[i:].
func row(f string, i, rows int) (int, string, bool) {
	j := i
	fixed := j < len(f) && f[j] == '$'
	if fixed {
		j++
	}
	k := j
	v := 0
	for k < len(f) && k-j < 7 && isDigit(f[k]) {
		v = v*10 + int(f[k]-'0')
		k++
	}
	if k == j || v < 1 || v > maxRow || k < len(f) && isDigit(f[k]) {
		return 0, "", false
	}
	if fixed {
		return k - i, f[i:k], true
	}
	v += rows
	if v < 1 || v > maxRow {
		return k - i, "#REF!", true
	}
	return k - i, itoa(v), true
}

// startsToken reports whether a reference or a name may begin at f[i]:
// what comes before it is not part of a word or a number.
func startsToken(f string, i int) bool {
	if i == 0 {
		return true
	}
	p := f[i-1]
	return !(isWord(p) || p == '.' || p == '$' || p == '\\' || p >= 0x80)
}

// ends reports whether a reference may end at f[i]: it is not followed by
// more of a word, nor by "(" (a function) or "!" (a sheet's name).
func ends(f string, i int) bool {
	if i >= len(f) {
		return true
	}
	c := f[i]
	return !(isWord(c) || c == '(' || c == '!' || c == '.' || c == '$' || c >= 0x80)
}

func columnName(v int) string {
	var b [3]byte
	n := len(b)
	for v > 0 {
		v--
		n--
		b[n] = byte('A' + v%26)
		v /= 26
	}
	return string(b[n:])
}

func itoa(v int) string {
	var b [8]byte
	n := len(b)
	for v > 0 {
		n--
		b[n] = byte('0' + v%10)
		v /= 10
	}
	return string(b[n:])
}

func isLetter(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isWord(c byte) bool   { return isLetter(c) || isDigit(c) || c == '_' }
func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}
