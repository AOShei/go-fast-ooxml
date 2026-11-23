package numfmt

import (
	"math"
	"math/bits"
	"strconv"
)

// decimal is a non-negative number as decimal digits: 0.d1d2d3... times
// ten to the power exp. It holds the fifteen significant digits a
// spreadsheet works with, which is why 0.1+0.2 shows as 0.3 and why 1.005
// rounds to 1.01: rounding is done on these digits, half away from zero,
// not on the binary value.
type decimal struct {
	d   [17]byte
	n   int
	exp int
}

func (x *decimal) set(v float64) {
	x.n, x.exp = 0, 0
	if v == 0 {
		return
	}
	if x.short(v) {
		return
	}
	x.long(v)
}

var tenTo = [...]float64{1, 10, 100, 1000, 10000}

// short sets x to v where v is a whole number, or one with up to four
// decimals, of fifteen digits at most: most of the numbers of a sheet.
// It reports false, having set nothing, for any other.
//
// The digits are those long gives. If v times a power of ten is a whole
// number w below 1e15, and w divided by that power is v again, then the
// decimal w over that power is one the binary value v is the nearest to,
// so v is within half a unit of its last binary place of it: far nearer
// than half a unit of the fifteenth decimal digit. It has fifteen digits
// at most, so it is the shortest decimal read back as v (no other decimal
// of fifteen digits or fewer is), which is what long gives.
func (x *decimal) short(v float64) bool {
	if !(v < 1e15) {
		return false // too large, an infinity or not a number
	}
	for k, p := range tenTo {
		w := v * p
		if w >= 1e15 {
			return false
		}
		if w != math.Trunc(w) {
			continue
		}
		u := uint64(w)
		if u == 0 || float64(u)/p != v {
			return false
		}
		var buf [20]byte
		s := strconv.AppendUint(buf[:0], u, 10)
		x.exp = len(s) - k
		n := len(s)
		for n > 0 && s[n-1] == '0' {
			n--
		}
		x.n = copy(x.d[:], s[:n])
		return true
	}
	return false
}

// long sets x to the fifteen significant digits of v by the general
// conversion, which short avoids for most numbers. As LibreOffice does,
// the digits are those of the shortest decimal that is read back as v,
// rounded half up to fifteen: 664.5600532184905 is 664.560053218491,
// although the binary value is a little under the decimal and rounded
// itself would end in 490.
func (x *decimal) long(v float64) {
	x.n, x.exp = 0, 0
	var buf [32]byte
	s := strconv.AppendFloat(buf[:0], v, 'e', -1, 64) // d[.ddd]e±xx, at most 17 digits
	x.d[0] = s[0]
	n := 1
	i := 1
	if i < len(s) && s[i] == '.' {
		i++
	}
	for ; i < len(s) && s[i] != 'e'; i++ {
		x.d[n] = s[i]
		n++
	}
	e, _ := strconv.Atoi(string(s[i+1:]))
	x.exp = e + 1
	if n > 15 {
		up := x.d[15] >= '5'
		n = 15
		if up {
			j := n - 1
			for ; j >= 0 && x.d[j] == '9'; j-- {
				x.d[j] = '0'
			}
			if j < 0 {
				// 9.99...95 and up: the digits carry into a new one.
				x.d[0] = '1'
				n = 1
				x.exp++
			} else {
				x.d[j]++
			}
		}
	}
	for n > 0 && x.d[n-1] == '0' {
		n--
	}
	x.n = n
}

// fixed writes x rounded to frac decimals into buf: the digits of the
// integer part, without leading zeros and empty for zero, then exactly
// frac digits.
func (x *decimal) fixed(buf []byte, frac int) (whole, part []byte) {
	intLen := max(x.exp, 0)
	total := intLen + frac
	if cap(buf) < total+1 {
		buf = make([]byte, 0, total+1)
	}
	buf = buf[:total+1]
	a := buf[1:] // buf[0] is room for a carry
	for j := range a {
		if i := x.exp - intLen + j; i >= 0 && i < x.n {
			a[j] = x.d[i]
		} else {
			a[j] = '0'
		}
	}
	if r := x.exp + frac; r >= 0 && r < x.n && x.d[r] >= '5' {
		j := total - 1
		for ; j >= 0; j-- {
			if a[j] != '9' {
				a[j]++
				break
			}
			a[j] = '0'
		}
		if j < 0 {
			buf[0] = '1'
			a = buf
		}
	}
	whole, part = a[:len(a)-frac], a[len(a)-frac:]
	for len(whole) > 0 && whole[0] == '0' {
		whole = whole[1:]
	}
	return whole, part
}

// AppendGeneral appends v in the General format: all of its digits, up to
// the fifteen a spreadsheet keeps, and scientific notation only where the
// plain form would be very long.
func AppendGeneral(dst []byte, v float64) []byte {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return append(dst, '0')
	}
	if v < 0 {
		dst = append(dst, '-')
		v = -v
	}
	var x decimal
	x.set(v)
	// Plain up to fifteen digits before the point. After it, plain if the
	// number is at least 0.0001; smaller ones are plain while that takes
	// no more than sixteen decimals, and down to a billionth.
	if x.exp > 15 || x.exp < -8 || x.exp < -3 && x.n-x.exp > 16 {
		dst = append(dst, x.d[0])
		if x.n > 1 {
			dst = append(dst, '.')
			dst = append(dst, x.d[1:x.n]...)
		}
		dst = append(dst, 'E')
		e := x.exp - 1
		if e < 0 {
			dst = append(dst, '-')
			e = -e
		} else {
			dst = append(dst, '+')
		}
		if e < 10 {
			dst = append(dst, '0')
		}
		return strconv.AppendInt(dst, int64(e), 10)
	}
	if x.exp <= 0 {
		dst = append(dst, '0', '.')
		for i := x.exp; i < 0; i++ {
			dst = append(dst, '0')
		}
		return append(dst, x.d[:x.n]...)
	}
	for i := 0; i < x.exp; i++ {
		if i < x.n {
			dst = append(dst, x.d[i])
		} else {
			dst = append(dst, '0')
		}
	}
	if x.n > x.exp {
		dst = append(dst, '.')
		dst = append(dst, x.d[x.exp:x.n]...)
	}
	return dst
}

// appendNumber renders v through a section of digit placeholders and
// literal text.
func (s *section) appendNumber(dst []byte, v float64, minus bool) []byte {
	if s.intSlots+s.fracSlots+s.numSlots+s.denSlots == 0 && s.fixedDen == 0 {
		// Text only: the number is not shown.
		for _, t := range s.toks {
			switch t.kind {
			case tokLiteral:
				dst = append(dst, t.text...)
			case tokPad:
				dst = append(dst, ' ')
			}
		}
		return dst
	}
	var x decimal
	x.set(math.Abs(v))
	if x.n > 0 {
		x.exp += s.shift
	}
	var scratch [96]byte
	var expText [8]byte
	zero := [1]byte{'0'}
	var whole, part, expDigits []byte
	var num, den int64
	expNeg := false
	switch {
	case s.exponent:
		// The exponent is a multiple of the number of integer
		// placeholders, which gives engineering notation for three.
		step := max(s.intSlots, 1)
		e := 0
		if x.n > 0 {
			e = floorMultiple(x.exp-1, step)
		}
		m := x
		m.exp = x.exp - e
		whole, part = m.fixed(scratch[:], s.fracSlots)
		if len(whole) > step {
			// Rounding carried into another digit.
			e += step
			m.exp = x.exp - e
			whole, part = m.fixed(scratch[:], s.fracSlots)
		}
		if len(whole) == 0 {
			whole = zero[:] // a mantissa always shows a digit
		}
		if e < 0 {
			expNeg, e = true, -e
		}
		expDigits = strconv.AppendInt(expText[:0], int64(e), 10)
	case s.fraction:
		var ok bool
		if whole, num, den, ok = s.fractionParts(&x, scratch[:]); !ok {
			// Too large to compute as a fraction: the number itself.
			return AppendGeneral(dst, v)
		}
	default:
		whole, part = x.fixed(scratch[:], s.fracSlots)
	}

	// A minus sign is written for a negative number unless everything
	// shown of it is zero.
	if minus && v < 0 && (nonZero(whole) || nonZero(part) || num != 0) {
		dst = append(dst, '-')
	}
	// How many decimals carry a digit that matters.
	lastFrac := len(part) - 1
	for lastFrac >= 0 && part[lastFrac] == '0' {
		lastFrac--
	}
	// The decimal point is written if a decimal is.
	pointShown := false
	fi := 0
	for _, t := range s.toks {
		if t.kind == tokFracDigit {
			if fi <= lastFrac || t.ch != '#' {
				pointShown = true
			}
			fi++
		}
	}
	hasFraction := num != 0
	intIndex, fracIndex, expIndex := 0, 0, 0
	var numText, denText [20]byte
	numDigits := strconv.AppendInt(numText[:0], num, 10)
	denDigits := strconv.AppendInt(denText[:0], den, 10)
	numIndex, denIndex := 0, 0
	for _, t := range s.toks {
		switch t.kind {
		case tokLiteral:
			dst = append(dst, t.text...)
		case tokPad:
			dst = append(dst, ' ')
		case tokDigit:
			dst = appendSlot(dst, whole, intIndex, s.intSlots, t.ch, s.thousands)
			intIndex++
		case tokPoint:
			if s.intSlots == 0 {
				dst = append(dst, whole...) // ".00" still shows the whole part
			}
			if pointShown {
				dst = append(dst, '.')
			}
		case tokFracDigit:
			switch {
			case fracIndex <= lastFrac || t.ch == '0':
				dst = append(dst, part[fracIndex])
			case t.ch == '?':
				dst = append(dst, ' ')
			}
			fracIndex++
		case tokExp:
			dst = append(dst, 'E')
			if expNeg {
				dst = append(dst, '-')
			} else if t.ch == '+' {
				dst = append(dst, '+')
			}
		case tokExpDigit:
			dst = appendSlot(dst, expDigits, expIndex, s.expSlots, t.ch, false)
			expIndex++
		case tokNumDigit:
			if hasFraction || s.intSlots == 0 {
				dst = appendSlot(dst, numDigits, numIndex, s.numSlots, t.ch, false)
			} else {
				dst = append(dst, ' ')
			}
			numIndex++
		case tokSlash:
			if hasFraction || s.intSlots == 0 {
				dst = append(dst, '/')
			} else {
				dst = append(dst, ' ')
			}
		case tokDenDigit:
			// A denominator starts at the bar: its digits fill the
			// placeholders from the left.
			switch {
			case !hasFraction && s.intSlots > 0:
				dst = append(dst, ' ')
			case denIndex == s.denSlots-1 && denIndex < len(denDigits):
				dst = append(dst, denDigits[denIndex:]...)
			case denIndex < len(denDigits):
				dst = append(dst, denDigits[denIndex])
			case t.ch == '0':
				dst = append(dst, '0')
			case t.ch == '?':
				dst = append(dst, ' ')
			}
			denIndex++
		case tokFixedDen:
			if hasFraction || s.intSlots == 0 {
				dst = strconv.AppendInt(dst, int64(t.n), 10)
			}
		}
	}
	return dst
}

// appendSlot writes what one digit placeholder shows. digits are the
// digits to place, right-aligned over slots placeholders; index is this
// placeholder's position from the left. The first placeholder takes every
// digit there is no placeholder for.
func appendSlot(dst, digits []byte, index, slots int, ch byte, thousands bool) []byte {
	// The digit this slot holds is digits[at]; negative means none.
	at := len(digits) - (slots - index)
	from := at
	if index == 0 && at > 0 {
		from = 0
	}
	if at < 0 {
		switch ch {
		case '0':
			dst = append(dst, '0')
			if thousands && (slots-index-1)%3 == 0 && slots-index-1 > 0 {
				dst = append(dst, ',')
			}
		case '?':
			dst = append(dst, ' ')
		}
		return dst
	}
	for i := from; i <= at; i++ {
		dst = append(dst, digits[i])
		if rest := len(digits) - 1 - i; thousands && rest > 0 && rest%3 == 0 {
			dst = append(dst, ',')
		}
	}
	return dst
}

func nonZero(digits []byte) bool {
	for _, c := range digits {
		if c != '0' {
			return true
		}
	}
	return false
}

// floorMultiple returns the largest multiple of step that is not above n.
func floorMultiple(n, step int) int {
	q := n / step
	if n%step != 0 && n < 0 {
		q--
	}
	return q * step
}

// fractionParts splits x into a whole number and a fraction for a section
// such as "# ?/?". With no integer placeholders the fraction is improper
// and whole is empty.
func (s *section) fractionParts(x *decimal, scratch []byte) (whole []byte, num, den int64, ok bool) {
	if x.exp > 16 {
		return nil, 0, 0, false
	}
	// The value as a whole part and fifteen decimals.
	w, part := x.fixed(scratch, 15)
	frac := int64(0)
	for _, c := range part {
		frac = frac*10 + int64(c-'0')
	}
	const scale = 1_000_000_000_000_000
	if s.fixedDen > 0 {
		den = int64(s.fixedDen)
		// Rounded to the nearest part; a value halfway goes down, as
		// LibreOffice has it.
		hi, lo := bits.Mul64(uint64(frac), uint64(den))
		q, rem := bits.Div64(hi, lo, scale) // hi is far below scale: frac < scale and den < 1e9
		num = int64(q)
		if rem*2 > scale {
			num++
		}
	} else {
		limit := int64(1)
		for i := 0; i < min(s.denSlots, 9); i++ {
			limit *= 10
		}
		num, den = closestFraction(frac, scale, limit-1)
	}
	carry := num >= den && den > 0
	if s.intSlots == 0 {
		// Improper: everything goes over the denominator. Whole parts too
		// large to multiply are left as they are.
		var wv int64
		for _, c := range w {
			wv = wv*10 + int64(c-'0')
		}
		if wv > (1<<62)/den {
			return nil, 0, 0, false
		}
		return nil, wv*den + num, den, true
	}
	if carry {
		// The fraction rounded up to one: it joins the whole part.
		num = 0
		whole = increment(scratch, w)
		return whole, num, den, true
	}
	if len(w) == 0 && num == 0 {
		// Nothing at all would be shown: the number is zero, and says so.
		w = append(scratch[:0], '0')
	}
	return w, num, den, true
}

// increment adds one to a string of decimal digits.
func increment(scratch, digits []byte) []byte {
	out := make([]byte, len(digits)+1)
	copy(out[1:], digits)
	out[0] = '0'
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != '9' {
			out[i]++
			break
		}
		out[i] = '0'
	}
	if out[0] == '0' {
		return out[1:]
	}
	return out
}

// closestFraction returns the fraction closest to n/d whose denominator
// is at most limit, by the continued fraction of n/d.
func closestFraction(n, d, limit int64) (int64, int64) {
	if n == 0 || limit < 1 {
		return 0, 1
	}
	p0, q0, p1, q1 := int64(0), int64(1), int64(1), int64(0)
	nn, dd := n, d
	for {
		a := nn / dd
		q2 := q0 + a*q1
		if q2 > limit {
			break
		}
		p0, q0, p1, q1 = p1, q1, p0+a*p1, q2
		nn, dd = dd, nn-a*dd
		if dd == 0 {
			return p1, q1
		}
	}
	// The two candidates: the last convergent, and the best fraction
	// between it and the one before.
	k := (limit - q0) / q1
	pa, qa := p0+k*p1, q0+k*q1
	pb, qb := p1, q1
	// Compare |n/d - pa/qa| with |n/d - pb/qb| in floating point: the
	// values are far enough apart for it.
	x := float64(n) / float64(d)
	if math.Abs(x-float64(pa)/float64(qa)) < math.Abs(x-float64(pb)/float64(qb)) {
		return pa, qa
	}
	return pb, qb
}

// appendElapsed renders a duration: a number of days as hours, minutes
// and seconds that do not wrap at a day.
func (s *section) appendElapsed(dst []byte, v float64) []byte {
	neg := v < 0
	v = math.Abs(v)
	// The value is rounded to the seconds shown, with their decimals, or
	// to the second if none are shown; minutes and hours are then whole
	// parts of that, not rounded again.
	scale := 1.0
	for i := 0; i < s.secDigits; i++ {
		scale *= 10
	}
	total := v * 86400 * scale
	if total > 9e15 {
		if neg {
			dst = append(dst, '-')
		}
		return AppendGeneral(dst, v)
	}
	units := int64(math.Floor(total + 0.5))
	if !s.hasSecs {
		units = int64(math.Floor(v*86400 + 0.5))
		scale = 1
	}
	// A duration that rounds to nothing has no sign, as LibreOffice
	// shows it: -1e-12 days is 0:00:00, not -0:00:00.
	if neg && units != 0 {
		dst = append(dst, '-')
	}
	fracPart := units % int64(scale)
	secs := units / int64(scale)
	// The largest total asked for decides what the rest are counted within.
	unit := byte(0)
	for _, t := range s.toks {
		if t.kind == tokElapsed && (unit == 0 || t.ch == 'h' || t.ch == 'm' && unit == 's') {
			unit = t.ch
		}
	}
	var hours, minutes, seconds int64
	switch unit {
	case 'h':
		hours, minutes, seconds = secs/3600, secs/60%60, secs%60
	case 'm':
		minutes, seconds = secs/60, secs%60
	default:
		seconds = secs
	}
	for _, t := range s.toks {
		switch t.kind {
		case tokLiteral:
			dst = append(dst, t.text...)
		case tokPad:
			dst = append(dst, ' ')
		case tokElapsed:
			switch t.ch {
			case 'h':
				dst = appendPadded(dst, hours, t.n)
			case 'm':
				dst = appendPadded(dst, minutes, t.n)
			default:
				dst = appendPadded(dst, seconds, t.n)
			}
		case tokHour:
			dst = appendPadded(dst, hours, t.n)
		case tokMinute:
			dst = appendPadded(dst, minutes, t.n)
		case tokSecond:
			dst = appendPadded(dst, seconds, t.n)
		case tokSecFrac:
			dst = append(dst, '.')
			dst = appendPadded(dst, fracPart, t.n)
		}
	}
	return dst
}

func appendPadded(dst []byte, v int64, width int) []byte {
	var buf [20]byte
	s := strconv.AppendInt(buf[:0], v, 10)
	for i := len(s); i < width && i < 18; i++ {
		dst = append(dst, '0')
	}
	return append(dst, s...)
}

// lastDay is the serial number of 9999-12-31, the last date a spreadsheet
// has.
const lastDay = 2958465

// appendISO writes a date serial number in ISO 8601: a date, a time of
// day, or both, according to what the value holds. A whole number of days
// is a date; a value under one day is a time; anything else is both. The
// second result is false if v is not a date the calendar has.
func appendISO(dst []byte, v float64, date1904, hasDate bool) ([]byte, bool) {
	if v < 0 || v >= lastDay+1 {
		return dst, false
	}
	// To the millisecond, which is finer than a spreadsheet shows.
	ms := int64(math.Floor(v*86400000 + 0.5))
	days, rest := ms/86400000, ms%86400000
	if days > lastDay {
		return dst, false
	}
	switch {
	case days == 0 && !(date1904 && rest == 0 && hasDate):
		return appendClock(dst, rest), true
	case rest == 0:
		return appendDay(dst, days, date1904), true
	}
	dst = appendDay(dst, days, date1904)
	dst = append(dst, 'T')
	return appendClock(dst, rest), true
}

func appendClock(dst []byte, ms int64) []byte {
	dst = appendPadded(dst, ms/3600000, 2)
	dst = append(dst, ':')
	dst = appendPadded(dst, ms/60000%60, 2)
	dst = append(dst, ':')
	dst = appendPadded(dst, ms/1000%60, 2)
	if frac := ms % 1000; frac != 0 {
		dst = append(dst, '.')
		dst = appendPadded(dst, frac, 3)
	}
	return dst
}

// appendDay writes the date of a day number. In the 1900 system day 1 is
// 1 January 1900 and day 60 is 29 February 1900, a day that never was:
// the first spreadsheet programs took 1900 for a leap year and the count
// has been kept. It is written as the file has it.
func appendDay(dst []byte, serial int64, date1904 bool) []byte {
	var days int64 // since 1970-01-01
	switch {
	case date1904:
		days = serial - 24107 // day 0 is 1904-01-01
	case serial == 60:
		return append(dst, "1900-02-29"...)
	case serial < 60:
		days = serial - 25568 // day 1 is 1900-01-01
	default:
		days = serial - 25569
	}
	// Civil date from a day count (the algorithm of Howard Hinnant).
	z := days + 719468
	era := z / 146097
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	dst = appendPadded(dst, y, 4)
	dst = append(dst, '-')
	dst = appendPadded(dst, m, 2)
	dst = append(dst, '-')
	return appendPadded(dst, d, 2)
}
