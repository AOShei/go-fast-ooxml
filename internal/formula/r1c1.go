package formula

import "strings"

// Excel's XML spreadsheet (SpreadsheetML 2003) writes formulas in R1C1
// notation: R2C3 is row 2, column 3 ($C$2); R[1]C[-1] is one row down and
// one column left of the formula's cell; RC is the cell itself; R2 and C3
// alone are a whole row and a whole column. FromR1C1 rewrites them in the
// A1 notation of every other format.

// FromR1C1 returns f, a formula in R1C1 notation written in the cell at
// row and col (from 0), with its references in A1 notation. A reference
// that falls off the sheet becomes #REF!, as in Excel. Everything else
// (strings, names, functions, numbers, sheet names) is left as it is.
func FromR1C1(f string, row, col int) string {
	var sb strings.Builder
	sb.Grow(len(f) + 8)
	i := 0
	for i < len(f) {
		c := f[i]
		switch {
		case c == '"' || c == '\'':
			// A string, or a quoted sheet name, with the quote doubled
			// inside it.
			j := i + 1
			for j < len(f) {
				if f[j] == c {
					if j+1 < len(f) && f[j+1] == c {
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
			// The name of another workbook.
			j := strings.IndexByte(f[i:], ']')
			if j < 0 {
				j = len(f) - i - 1
			}
			sb.WriteString(f[i : i+j+1])
			i += j + 1
		case c == '#':
			j := i + 1
			for j < len(f) && (isWord(f[j]) || f[j] == '/' || f[j] == '!' || f[j] == '?') {
				j++
			}
			sb.WriteString(f[i:j])
			i = j
		case startsToken(f, i) && (c == 'R' || c == 'C'):
			if n, out := r1c1Reference(f, i, row, col); n > 0 {
				sb.WriteString(out)
				i += n
				continue
			}
			fallthrough
		case startsToken(f, i) && (isLetter(c) || isDigit(c) || c == '_' || c >= 0x80):
			j := i + 1
			for j < len(f) && (isWord(f[j]) || f[j] == '.' || f[j] == '\\' || f[j] >= 0x80) {
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

// part is the row or the column of an R1C1 reference.
type part struct {
	has      bool
	absolute bool
	v        int // the number, or the offset of a relative part
}

// r1c1 is one reference: a cell, a whole row, or a whole column.
type r1c1 struct{ row, col part }

// readPart reads "R", "R5" or "R[-2]" (letter 'R'), or the same for 'C',
// at f[i:], and returns how many bytes it takes.
func readPart(f string, i int, letter byte) (int, part) {
	if i >= len(f) || f[i] != letter {
		return 0, part{}
	}
	j := i + 1
	switch {
	case j < len(f) && f[j] == '[':
		k, neg := j+1, false
		if k < len(f) && (f[k] == '-' || f[k] == '+') {
			neg = f[k] == '-'
			k++
		}
		start, v := k, 0
		for k < len(f) && isDigit(f[k]) && k-start < 8 {
			v = v*10 + int(f[k]-'0')
			k++
		}
		if k == start || k >= len(f) || f[k] != ']' {
			return 0, part{}
		}
		if neg {
			v = -v
		}
		return k + 1 - i, part{has: true, v: v}
	case j < len(f) && isDigit(f[j]):
		k, v := j, 0
		for k < len(f) && isDigit(f[k]) && k-j < 8 {
			v = v*10 + int(f[k]-'0')
			k++
		}
		if k < len(f) && isDigit(f[k]) {
			return 0, part{}
		}
		return k - i, part{has: true, absolute: true, v: v}
	}
	return 1, part{has: true} // the formula's own row or column
}

// readR1C1 reads one reference at f[i:] and returns how many bytes it
// takes, or 0 if there is none there.
func readR1C1(f string, i int) (int, r1c1) {
	var r r1c1
	n, p := readPart(f, i, 'R')
	if n > 0 {
		r.row = p
	}
	m, q := readPart(f, i+n, 'C')
	if m > 0 {
		r.col = q
	}
	if n+m == 0 || !ends(f, i+n+m) {
		return 0, r1c1{}
	}
	return n + m, r
}

// resolve returns the place a part names (from 0), and whether it is on
// the sheet.
func (p part) resolve(base, limit int) (int, bool) {
	v := base + p.v
	if p.absolute {
		v = p.v - 1
	}
	return v, v >= 0 && v < limit
}

// a1 writes p in A1 notation, a column if col is set.
func (p part) a1(sb *strings.Builder, base int, col bool) bool {
	limit := maxRow
	if col {
		limit = maxCol
	}
	v, ok := p.resolve(base, limit)
	if !ok {
		return false
	}
	if p.absolute {
		sb.WriteByte('$')
	}
	if col {
		sb.WriteString(columnName(v + 1))
	} else {
		sb.WriteString(itoa(v + 1))
	}
	return true
}

// r1c1Reference reads a reference, or a range of two, at f[i:], and
// returns how many bytes it takes and the reference in A1 notation.
func r1c1Reference(f string, i, row, col int) (int, string) {
	n, a := readR1C1(f, i)
	if n == 0 {
		return 0, ""
	}
	b, two := a, false
	if i+n < len(f) && f[i+n] == ':' {
		if m, second := readR1C1(f, i+n+1); m > 0 && second.row.has == a.row.has && second.col.has == a.col.has {
			b, two = second, true
			n += 1 + m
		}
	}
	var sb strings.Builder
	write := func(r r1c1) bool {
		ok := true
		if r.col.has {
			ok = r.col.a1(&sb, col, true)
		}
		if ok && r.row.has {
			ok = r.row.a1(&sb, row, false)
		}
		return ok
	}
	whole := !a.row.has || !a.col.has // rows or columns: always a range in A1
	if !write(a) {
		return n, "#REF!"
	}
	if two || whole {
		sb.WriteByte(':')
		if !write(b) {
			return n, "#REF!"
		}
	}
	return n, sb.String()
}

// R1C1Range reads a range in R1C1 notation, as SpreadsheetML 2003 writes
// the cells a drop-down list is for: "R2C1:R10C1", "C3" (a whole column),
// "R1C1". Relative parts count from row and col. It returns the corners,
// from 0, both included.
func R1C1Range(s string, row, col int) (r0, c0, r1, c1 int, ok bool) {
	s = strings.TrimSpace(s)
	n, a := readR1C1(s, 0)
	if n == 0 {
		return 0, 0, 0, 0, false
	}
	b := a
	if n < len(s) && s[n] == ':' {
		m, second := readR1C1(s, n+1)
		if m == 0 {
			return 0, 0, 0, 0, false
		}
		b, n = second, n+1+m
	}
	if n != len(s) {
		return 0, 0, 0, 0, false
	}
	corner := func(r r1c1, end bool) (int, int, bool) {
		y, x := 0, 0
		if end {
			y, x = maxRow-1, maxCol-1
		}
		ok := true
		if r.row.has {
			y, ok = r.row.resolve(row, maxRow)
		}
		if ok && r.col.has {
			x, ok = r.col.resolve(col, maxCol)
		}
		return y, x, ok
	}
	r0, c0, ok0 := corner(a, false)
	r1, c1, ok1 := corner(b, true)
	if !ok0 || !ok1 {
		return 0, 0, 0, 0, false
	}
	return min(r0, r1), min(c0, c1), max(r0, r1), max(c0, c1), true
}
