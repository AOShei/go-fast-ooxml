package sml

import (
	"bytes"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Conditional formatting gives cells a colour of text or a fill that
// depends on their values: white text where the value is zero, a fill
// from a colour scale. Whether a cell's text can be seen is judged with
// the colours the rules that match it give, as with the colours of its
// style. A rule matters only if it gives a colour; one that makes text
// bold or draws a data bar changes nothing here.
//
// The rules that can be decided from the values the file stores are:
// comparisons with constants, text tests, blanks and errors, duplicates,
// top and bottom N, above and below average, and colour scales. Rules
// that are formulas, and those of dates relative to today, are not
// evaluated: where it makes a difference whether one matches, the cell
// is not judged, and a warning counts it.
//
// The values are read in a pass over the sheet's rows of their own,
// which is made only for a sheet with rules that can be decided and give
// a colour. It is the same pass whether the sheet is read whole or a
// band at a time, so both judge alike.

// Kinds of rule.
const (
	cfCellIs = iota + 1
	cfContains
	cfNotContains
	cfBegins
	cfEnds
	cfBlanks
	cfNotBlanks
	cfErrors
	cfNotErrors
	cfDuplicate
	cfUnique
	cfTop
	cfAverage
	cfScale
	cfUndecided // a formula, a date relative to today, or a kind not known
)

// cfRule is one rule that gives a colour.
type cfRule struct {
	ranges   []rect
	kind     uint8
	priority int
	stop     bool // stop if true: rules of lower priority do not apply
	look     look // what it gives where it matches

	op       string     // of cellIs
	operands [2]cfValue // of cellIs
	text     string     // of the text tests, in lower case

	rank           float64 // of top10
	percent, below bool    // of top10 (below: bottom), and of aboveAverage
	equal          bool    // of aboveAverage
	stdDev         float64 // of aboveAverage

	scale []cfStop // of a colour scale

	// Worked out from the values of the rule's cells.
	threshold float64
	counts    map[string]int
	low, high float64
}

// cfStop is a stop of a colour scale: where it is and its colour.
type cfStop struct {
	kind  string // min, max, num, percent, percentile
	val   float64
	color theme.Color
	at    float64 // worked out
}

// cfValue is a value as conditional formatting compares it.
type cfValue struct {
	kind uint8 // 0 none, 1 number, 2 text, 3 boolean, 4 error
	num  float64
	text string
}

// Bounds on what conditional formatting may cost.
const (
	maxRules       = 64      // rules that give a colour, on one sheet
	maxCFValues    = 1 << 20 // values read for them
	maxUndecided   = 6       // undecided rules over one cell that are tried both ways
	maxCFJudgement = 1 << 24 // cells times ranges looked at
)

// conditional is what a sheet's conditional formatting comes to.
type conditional struct {
	rules   []cfRule
	opaque  []rect // the ranges of rules beyond the bound: their cells are not judged
	all     bool   // a range could not be read: every cell may be affected
	done    bool   // the values have been read
	matched map[pos]uint64
	scale   map[scaled]theme.Color // the fill each colour scale gives a cell
	failed  bool                   // the values could not be read: rules that need them are undecided
}

// scaled is a cell under a colour scale.
type scaled struct {
	at   pos
	rule int
}

// readConditional reads conditionalFormatting: the rules for its ranges.
func (sh *sheet) readConditional(s *xmlscan.Scanner) {
	v, _ := s.Attr(xmlscan.NoSpace, "sqref")
	ranges, ok := sh.cfRanges(v)
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || !s.Is(ns.Sheet, "cfRule") {
			continue
		}
		r, keep := sh.readRule(s)
		if !keep {
			continue
		}
		if !ok {
			sh.cond.all = true
			continue
		}
		r.ranges = ranges
		sh.addRule(r)
	}
}

// addRule keeps a rule. Past the bound on rules, its cells are not
// judged.
func (sh *sheet) addRule(r cfRule) {
	c := &sh.cond
	if len(c.rules) < maxRules {
		c.rules = append(c.rules, r)
		return
	}
	if len(c.opaque)+len(r.ranges) > maxVarying {
		c.all = true
		return
	}
	c.opaque = append(c.opaque, r.ranges...)
}

// cfRanges reads a list of ranges such as "A1:B9 D4".
func (sh *sheet) cfRanges(sqref []byte) ([]rect, bool) {
	var out []rect
	for _, ref := range bytes.Fields(sqref) {
		r, ok := parseRange(ref)
		if !ok || len(out) >= maxVarying {
			return nil, false
		}
		out = append(out, r)
	}
	return out, len(out) > 0
}

// readRule reads a cfRule. keep says it can give a colour.
func (sh *sheet) readRule(s *xmlscan.Scanner) (r cfRule, keep bool) {
	typ, _ := s.Attr(xmlscan.NoSpace, "type")
	kind := string(typ)
	r.priority, _ = intAttr(s, "priority")
	if v, ok := s.Attr(xmlscan.NoSpace, "stopIfTrue"); ok {
		r.stop = isTrue(v)
	}
	if id, ok := intAttr(s, "dxfId"); ok && sh.wb.styles != nil && id < len(sh.wb.styles.dxfs) {
		r.look = sh.wb.styles.dxfs[id]
	}
	attr := func(name string) string {
		v, _ := s.Attr(xmlscan.NoSpace, name)
		return string(v)
	}
	switch kind {
	case "cellIs":
		r.kind, r.op = cfCellIs, attr("operator")
	case "containsText":
		r.kind, r.text = cfContains, strings.ToLower(attr("text"))
	case "notContainsText":
		r.kind, r.text = cfNotContains, strings.ToLower(attr("text"))
	case "beginsWith":
		r.kind, r.text = cfBegins, strings.ToLower(attr("text"))
	case "endsWith":
		r.kind, r.text = cfEnds, strings.ToLower(attr("text"))
	case "containsBlanks":
		r.kind = cfBlanks
	case "notContainsBlanks":
		r.kind = cfNotBlanks
	case "containsErrors":
		r.kind = cfErrors
	case "notContainsErrors":
		r.kind = cfNotErrors
	case "duplicateValues":
		r.kind = cfDuplicate
	case "uniqueValues":
		r.kind = cfUnique
	case "top10":
		r.kind = cfTop
		r.rank = 10
		if v, err := strconv.ParseFloat(attr("rank"), 64); err == nil && v >= 0 {
			r.rank = v
		}
		r.percent, r.below = isTrue([]byte(attr("percent"))), isTrue([]byte(attr("bottom")))
	case "aboveAverage":
		r.kind = cfAverage
		if v, ok := s.Attr(xmlscan.NoSpace, "aboveAverage"); ok {
			r.below = !isTrue(v)
		}
		r.equal = isTrue([]byte(attr("equalAverage")))
		if v, err := strconv.ParseFloat(attr("stdDev"), 64); err == nil && v > 0 {
			r.stdDev = v
		}
	case "colorScale":
		r.kind = cfScale
	case "dataBar", "iconSet":
		// They draw beside the text, and change neither of its colours.
		s.Skip()
		return r, false
	default:
		r.kind = cfUndecided // expression, timePeriod, and kinds not known
	}
	// What the rule's text tests use wildcards for is not done here.
	if r.kind >= cfContains && r.kind <= cfEnds && strings.ContainsAny(r.text, "*?~") {
		r.kind = cfUndecided
	}
	var formulas []string
	var colors []theme.Color
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Sheet, "formula") && len(formulas) < 3:
			sh.raw = s.AppendText(sh.raw[:0])
			formulas = append(formulas, string(bytes.TrimSpace(sh.raw)))
		case s.Is(ns.Sheet, "cfvo") && len(r.scale) < 3:
			st := cfStop{kind: attr("type")}
			if st.kind != "min" && st.kind != "max" {
				val, err := strconv.ParseFloat(attr("val"), 64)
				if err != nil || st.kind == "formula" || st.kind != "num" && st.kind != "percent" && st.kind != "percentile" {
					r.kind = cfUndecided
				}
				st.val = val
			}
			r.scale = append(r.scale, st)
		case s.Is(ns.Sheet, "color") && r.kind == cfScale && len(colors) < 3:
			c, ok := sh.wb.styles.resolveRef(readColorRef(s))
			if !ok {
				r.kind = cfUndecided
			}
			colors = append(colors, c)
		}
	}
	switch r.kind {
	case cfCellIs:
		// Only comparisons with constants are decided.
		n := 1
		if r.op == "between" || r.op == "notBetween" {
			n = 2
		}
		for i := 0; i < n; i++ {
			v, ok := constant(formulas, i)
			if !ok {
				r.kind = cfUndecided
				break
			}
			r.operands[i] = v
		}
	}
	if kind == "colorScale" {
		if r.kind != cfScale || len(r.scale) < 2 || len(colors) != len(r.scale) {
			// A scale whose stops are not worked out: it gives a fill
			// that is not known.
			r.kind, r.look = cfUndecided, look{fill: 2}
		} else {
			for i := range r.scale {
				r.scale[i].color = colors[i]
			}
		}
		return r, true
	}
	// A rule that stops the rules after it matters even with no colour.
	return r, r.look.inkSet || r.look.fill != 0 || r.stop
}

// resolveRef resolves a colour against the workbook's theme and palette.
func (st *styleSheet) resolveRef(c colorRef) (theme.Color, bool) {
	if st == nil || st.resolve == nil {
		return theme.Color{}, false
	}
	return st.resolve(c)
}

// constant reads the formula of a comparison when it is a constant: a
// number, a string in quotes, TRUE or FALSE.
func constant(formulas []string, i int) (cfValue, bool) {
	if i >= len(formulas) {
		return cfValue{}, false
	}
	f := strings.TrimPrefix(formulas[i], "=")
	switch {
	case len(f) >= 2 && f[0] == '"' && f[len(f)-1] == '"':
		inner := f[1 : len(f)-1]
		if strings.Count(strings.ReplaceAll(inner, `""`, ""), `"`) > 0 {
			return cfValue{}, false
		}
		return cfValue{kind: 2, text: strings.ReplaceAll(inner, `""`, `"`)}, true
	case strings.EqualFold(f, "TRUE"):
		return cfValue{kind: 3, num: 1}, true
	case strings.EqualFold(f, "FALSE"):
		return cfValue{kind: 3}, true
	}
	if v, err := strconv.ParseFloat(f, 64); err == nil && !math.IsInf(v, 0) && !math.IsNaN(v) {
		return cfValue{kind: 1, num: v}, true
	}
	return cfValue{}, false
}

// readExtConditional reads conditional formatting kept in the sheet's
// extension list, where Excel puts the kinds it added later. Data bars
// and icon sets change no colour of text; the other rules there are not
// decided.
func (sh *sheet) readExtConditional(s *xmlscan.Scanner) {
	depth := s.Depth()
	colors := false
	var ranges []rect
	ok := true
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch string(s.Local()) {
		case "cfRule":
			if t, _ := s.Attr(xmlscan.NoSpace, "type"); string(t) != "dataBar" && string(t) != "iconSet" {
				colors = true
			}
			s.Skip()
		case "sqref":
			sh.raw = s.AppendText(sh.raw[:0])
			ranges, ok = sh.cfRanges(sh.raw)
		}
	}
	switch {
	case !colors:
	case !ok:
		sh.cond.all = true
	default:
		sh.addRule(cfRule{ranges: ranges, kind: cfUndecided, priority: math.MaxInt32, look: look{fill: 2}})
	}
}

// covers reports whether the rule's ranges hold the cell.
func (r *cfRule) covers(row, col int32) bool {
	for _, g := range r.ranges {
		if row >= g.r0 && row <= g.r1 && col >= g.c0 && col <= g.c1 {
			return true
		}
	}
	return false
}

// needsValues reports whether a rule is decided from values.
func (r *cfRule) needsValues() bool { return r.kind != cfUndecided }

// aggregate reports whether a rule is decided from the values of all its
// cells, not only the cell's own.
func (r *cfRule) aggregate() bool {
	switch r.kind {
	case cfDuplicate, cfUnique, cfTop, cfAverage, cfScale:
		return true
	}
	return false
}

// valuedCell is a cell's value, read for conditional formatting.
type valuedCell struct {
	row, col int32
	v        cfValue
}

// readValues reads the values of the cells under the rules that are
// decided from values, and works out which rules each cell matches.
func (sh *sheet) readValues() {
	c := &sh.cond
	c.done = true
	need := false
	for i := range c.rules {
		need = need || c.rules[i].needsValues()
	}
	if !need {
		return
	}
	cells, ok := sh.scanValues()
	if !ok {
		c.failed = true
		return
	}
	// What rules of all their cells need.
	for i := range c.rules {
		r := &c.rules[i]
		if !r.aggregate() {
			continue
		}
		var nums []float64
		if r.kind == cfDuplicate || r.kind == cfUnique {
			r.counts = map[string]int{}
		}
		for k := range cells {
			vc := &cells[k]
			if !r.covers(vc.row, vc.col) {
				continue
			}
			if r.counts != nil {
				if key, ok := vc.v.key(); ok {
					r.counts[key]++
				}
			} else if vc.v.kind == 1 {
				nums = append(nums, vc.v.num)
			}
		}
		r.summarize(nums)
	}
	c.matched = map[pos]uint64{}
	for k := range cells {
		vc := &cells[k]
		var mask uint64
		for i := range c.rules {
			r := &c.rules[i]
			if !r.needsValues() || !r.covers(vc.row, vc.col) {
				continue
			}
			if r.kind == cfScale {
				if fill, ok := r.scaleColor(vc.v); ok {
					if c.scale == nil {
						c.scale = map[scaled]theme.Color{}
					}
					c.scale[scaled{pos{vc.row, vc.col}, i}] = fill
					mask |= 1 << i
				}
				continue
			}
			if r.matches(vc.v) {
				mask |= 1 << i
			}
		}
		if mask != 0 {
			c.matched[pos{vc.row, vc.col}] = mask
		}
	}
}

// summarize works out what a rule needs of all its cells' numbers.
func (r *cfRule) summarize(nums []float64) {
	if len(nums) == 0 {
		return
	}
	switch r.kind {
	case cfTop:
		sort.Float64s(nums)
		n := r.rank
		if r.percent {
			n = math.Floor(float64(len(nums)) * r.rank / 100)
		}
		k := min(max(int(n), 1), len(nums))
		if r.below {
			r.threshold = nums[k-1]
		} else {
			r.threshold = nums[len(nums)-k]
		}
	case cfAverage:
		sum := 0.0
		for _, v := range nums {
			sum += v
		}
		mean := sum / float64(len(nums))
		r.threshold = mean
		if r.stdDev > 0 {
			sq := 0.0
			for _, v := range nums {
				sq += (v - mean) * (v - mean)
			}
			sd := 0.0
			if len(nums) > 1 {
				sd = math.Sqrt(sq / float64(len(nums)-1))
			}
			if r.below {
				r.threshold = mean - r.stdDev*sd
			} else {
				r.threshold = mean + r.stdDev*sd
			}
		}
	case cfScale:
		sort.Float64s(nums)
		r.low, r.high = nums[0], nums[len(nums)-1]
		for i := range r.scale {
			st := &r.scale[i]
			switch st.kind {
			case "min":
				st.at = r.low
			case "max":
				st.at = r.high
			case "num":
				st.at = st.val
			case "percent":
				st.at = r.low + (r.high-r.low)*st.val/100
			case "percentile":
				st.at = percentile(nums, st.val)
			}
		}
	}
}

// percentile is Excel's PERCENTILE.INC of sorted numbers.
func percentile(sorted []float64, p float64) float64 {
	p = min(max(p, 0), 100) / 100
	at := p * float64(len(sorted)-1)
	i := int(at)
	if i+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[i] + (sorted[i+1]-sorted[i])*(at-float64(i))
}

// scaleColor returns the fill a colour scale gives a value: a number
// between two stops is given the colour between theirs.
func (r *cfRule) scaleColor(v cfValue) (theme.Color, bool) {
	if v.kind != 1 || len(r.scale) < 2 {
		return theme.Color{}, false
	}
	stops := r.scale
	if v.num <= stops[0].at {
		return stops[0].color, true
	}
	for i := 1; i < len(stops); i++ {
		lo, hi := stops[i-1], stops[i]
		if v.num <= hi.at {
			t := 0.0
			if hi.at > lo.at {
				t = (v.num - lo.at) / (hi.at - lo.at)
			}
			mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a) + (float64(b)-float64(a))*t)) }
			return theme.Color{R: mix(lo.color.R, hi.color.R), G: mix(lo.color.G, hi.color.G), B: mix(lo.color.B, hi.color.B)}, true
		}
	}
	return stops[len(stops)-1].color, true
}

// key is what makes two values the same for duplicates: numbers by
// value, text without regard to case. Errors are not counted.
func (v cfValue) key() (string, bool) {
	switch v.kind {
	case 1:
		return "n" + strconv.FormatFloat(v.num, 'g', -1, 64), true
	case 2:
		return "s" + strings.ToLower(v.text), true
	case 3:
		return "b" + strconv.FormatFloat(v.num, 'g', -1, 64), true
	}
	return "", false
}

// asText is a value as the text tests see it: a number in Excel's
// General form, without its cell's format.
func (v cfValue) asText() string {
	switch v.kind {
	case 1:
		return strconv.FormatFloat(v.num, 'G', 15, 64)
	case 2:
		return strings.ToLower(v.text)
	case 3:
		if v.num != 0 {
			return "true"
		}
		return "false"
	}
	return ""
}

// compare orders two values as Excel does: numbers before text, text
// before booleans; text without regard to case.
func compare(a, b cfValue) int {
	if a.kind != b.kind {
		order := map[uint8]int{1: 0, 2: 1, 3: 2}
		return order[a.kind] - order[b.kind]
	}
	if a.kind == 2 {
		return strings.Compare(strings.ToLower(a.text), strings.ToLower(b.text))
	}
	switch {
	case a.num < b.num:
		return -1
	case a.num > b.num:
		return 1
	}
	return 0
}

// matches reports whether a rule that is not a colour scale matches a
// cell's value.
func (r *cfRule) matches(v cfValue) bool {
	blank := v.kind == 0 || v.kind == 2 && strings.TrimSpace(v.text) == ""
	switch r.kind {
	case cfBlanks:
		return blank
	case cfNotBlanks:
		return !blank
	case cfErrors:
		return v.kind == 4
	case cfNotErrors:
		return v.kind != 4
	}
	if v.kind == 4 || v.kind == 0 {
		return false // a rule over an error is an error, which does not match
	}
	switch r.kind {
	case cfCellIs:
		c1 := compare(v, r.operands[0])
		switch r.op {
		case "lessThan":
			return c1 < 0
		case "lessThanOrEqual":
			return c1 <= 0
		case "equal":
			return c1 == 0
		case "notEqual":
			return c1 != 0
		case "greaterThanOrEqual":
			return c1 >= 0
		case "greaterThan":
			return c1 > 0
		case "between", "notBetween":
			lo, hi := r.operands[0], r.operands[1]
			if compare(lo, hi) > 0 {
				lo, hi = hi, lo
			}
			in := compare(v, lo) >= 0 && compare(v, hi) <= 0
			return in == (r.op == "between")
		}
		return false
	case cfContains:
		return strings.Contains(v.asText(), r.text)
	case cfNotContains:
		return !strings.Contains(v.asText(), r.text)
	case cfBegins:
		return strings.HasPrefix(v.asText(), r.text)
	case cfEnds:
		return strings.HasSuffix(v.asText(), r.text)
	case cfDuplicate, cfUnique:
		key, ok := v.key()
		if !ok {
			return false
		}
		return (r.counts[key] > 1) == (r.kind == cfDuplicate)
	case cfTop:
		if v.kind != 1 {
			return false
		}
		if r.below {
			return v.num <= r.threshold
		}
		return v.num >= r.threshold
	case cfAverage:
		if v.kind != 1 {
			return false
		}
		switch {
		case r.below && r.equal:
			return v.num <= r.threshold
		case r.below:
			return v.num < r.threshold
		case r.equal:
			return v.num >= r.threshold
		}
		return v.num > r.threshold
	}
	return false
}

// scanValues reads the values of the cells under the rules decided from
// values, in a pass of its own over the sheet's rows. ok is false if the
// part could not be read, or holds more such cells than are read.
func (sh *sheet) scanValues() (cells []valuedCell, ok bool) {
	var box rect
	first := true
	for i := range sh.cond.rules {
		r := &sh.cond.rules[i]
		if !r.needsValues() {
			continue
		}
		for _, g := range r.ranges {
			if first {
				box, first = g, false
				continue
			}
			box = rect{min(box.r0, g.r0), min(box.c0, g.c0), max(box.r1, g.r1), max(box.c1, g.c1)}
		}
	}
	var s xmlscan.Scanner
	closer, err := sh.wb.pkg.Scan(sh.name, &s, scanOptions)
	if err != nil {
		return nil, false
	}
	defer closer.Close()
	var raw []byte
	row := int32(-1)
	inData := false
	for s.Next() != xmlscan.EOF {
		if s.Kind() == xmlscan.End && inData && s.Depth() < 2 {
			break // past sheetData
		}
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Depth() == 2 && s.Is(ns.Sheet, "sheetData"):
			inData = true
		case s.Depth() == 2:
			s.Skip()
		case s.Depth() == 3 && s.Is(ns.Sheet, "row"):
			row++
			if v, ok := s.Attr(xmlscan.NoSpace, "r"); ok {
				if r, ok := atoi(v); ok && r >= 1 {
					row = int32(min(r, maxRows+1) - 1)
				}
			}
			if row < box.r0 || row > box.r1 {
				s.Skip()
				continue
			}
			col := int32(-1)
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || !s.Is(ns.Sheet, "c") {
					if s.Kind() == xmlscan.Start {
						s.Skip()
					}
					continue
				}
				col++
				if v, ok := s.Attr(xmlscan.NoSpace, "r"); ok {
					if c, _, ok := parseRef(v); ok {
						col = c
					}
				}
				typ := byte('n')
				if v, ok := s.Attr(xmlscan.NoSpace, "t"); ok && len(v) > 0 {
					typ = v[0]
					if string(v) == "str" {
						typ = 't'
					}
				}
				if col < box.c0 || col > box.c1 {
					s.Skip()
					continue
				}
				raw = raw[:0]
				has := false
				cellDepth := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= cellDepth {
					if s.Kind() != xmlscan.Start {
						continue
					}
					switch {
					case s.Is(ns.Sheet, "v"):
						raw, has = s.AppendText(raw), true
					case s.Is(ns.Sheet, "is"):
						raw, has = readRichText(&s, raw), true
					default:
						s.Skip()
					}
				}
				if !has {
					continue
				}
				v, ok := sh.cfValueOf(typ, raw)
				if !ok {
					continue
				}
				if len(cells) >= maxCFValues {
					return nil, false
				}
				cells = append(cells, valuedCell{row: row, col: col, v: v})
			}
		}
	}
	return cells, s.Err() == nil
}

// cfValueOf turns a cell's stored value into what conditional formatting
// compares.
func (sh *sheet) cfValueOf(typ byte, raw []byte) (cfValue, bool) {
	switch typ {
	case 's':
		i, ok := atoi(bytes.TrimSpace(raw))
		if !ok {
			return cfValue{}, false
		}
		text, _ := sh.wb.strings.at(i)
		return cfValue{kind: 2, text: text}, true
	case 'i', 't':
		return cfValue{kind: 2, text: string(raw)}, true
	case 'b':
		v := bytes.TrimSpace(raw)
		if len(v) == 1 && v[0] == '1' || string(v) == "true" {
			return cfValue{kind: 3, num: 1}, true
		}
		return cfValue{kind: 3}, true
	case 'e':
		return cfValue{kind: 4}, true
	case 'd':
		return cfValue{}, false // a date as text: not compared
	}
	f, err := strconv.ParseFloat(string(bytes.TrimSpace(raw)), 64)
	if err != nil {
		return cfValue{}, false
	}
	return cfValue{kind: 1, num: f}, true
}

// applyConditional judges again whether the text of each cell that
// conditional formatting may change can be seen, with the colours the
// rules that match it give. tables are the sheet's styled tables, whose
// colours are under the rules'.
func (sh *sheet) applyConditional(tables []declaredTable) {
	c := &sh.cond
	if len(c.rules) == 0 && len(c.opaque) == 0 && !c.all {
		return
	}
	if !c.done {
		sh.readValues()
	}
	// Rules in the order they apply: the highest priority (the lowest
	// number) first.
	order := make([]int, len(c.rules))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return c.rules[a].priority - c.rules[b].priority })
	ranges := len(c.opaque)
	for i := range c.rules {
		ranges += len(c.rules[i].ranges)
	}
	tooMany := len(sh.cells)*max(ranges, 1) > maxCFJudgement
	unjudged := 0
	var over []int
	for i := range sh.cells {
		cell := &sh.cells[i]
		over = over[:0]
		unknown := c.all || tooMany
		if !unknown {
			for _, g := range c.opaque {
				if cell.row >= g.r0 && cell.row <= g.r1 && cell.col >= g.c0 && cell.col <= g.c1 {
					unknown = true
					break
				}
			}
		}
		if !unknown {
			for _, k := range order {
				if c.rules[k].covers(cell.row, cell.col) {
					over = append(over, k)
				}
			}
			if len(over) == 0 {
				continue
			}
		}
		own := sh.wb.styles.style(int(cell.style))
		table := sh.tableLook(tables, cell.row, cell.col)
		low, known := sh.judgeConditional(cell, own, table, over, unknown)
		cell.hidden &^= gofastcore.HiddenLowContrast
		switch {
		case !known:
			unjudged++
		case low:
			cell.hidden |= gofastcore.HiddenLowContrast
		}
	}
	if unjudged > 0 {
		sh.part.Warn(gofastcore.SeverityWarning, warn.HiddenUnchecked, strconv.Itoa(unjudged)+
			" cells have colours that conditional formatting gives by rules that are formulas, or that could not be worked out; whether their text can be seen against its fill was not judged")
	}
}

// judgeConditional judges a cell under the rules over it (in order of
// priority). A rule not decided is tried matching and not matching: if
// that makes a difference, the cell is not judged (known is false).
func (sh *sheet) judgeConditional(cell *cell, own *cellStyle, table look, over []int, unknown bool) (low, known bool) {
	c := &sh.cond
	if unknown {
		return false, false
	}
	at := pos{cell.row, cell.col}
	mask := c.matched[at]
	var undecided []int
	for _, k := range over {
		if !c.rules[k].needsValues() || c.failed {
			undecided = append(undecided, k)
		}
	}
	if len(undecided) > maxUndecided {
		return false, false
	}
	first := true
	for try := 0; try < 1<<len(undecided); try++ {
		m := mask
		for j, k := range undecided {
			if try&(1<<j) != 0 {
				m |= 1 << k
			} else {
				m &^= 1 << k
			}
		}
		l, ok := sh.lookUnder(own, table, over, m, at)
		if !ok {
			return false, false
		}
		if first {
			low, first = l, false
		} else if l != low {
			return false, false
		}
	}
	return low, true
}

// lookUnder judges a cell whose matching rules are those in mask. ok is
// false where a colour is not known.
func (sh *sheet) lookUnder(own *cellStyle, table look, over []int, mask uint64, at pos) (low, ok bool) {
	c := &sh.cond
	var given look
	inkGiven, fillGiven := false, false
	for _, k := range over {
		if mask&(1<<k) == 0 {
			continue
		}
		r := &c.rules[k]
		l := r.look
		if r.kind == cfScale {
			l = look{fill: 1, fillColor: c.scale[scaled{at, k}]}
		}
		if !inkGiven && l.inkSet {
			given.inkSet, given.ink, inkGiven = true, l.ink, true
		}
		if !fillGiven && l.fill != 0 {
			given.fill, given.fillColor, fillGiven = l.fill, l.fillColor, true
		}
		if r.stop {
			break
		}
	}
	ink, inkSet := given.ink, given.inkSet
	if !inkSet {
		ink, inkSet = own.ink, own.inkSet
		if !inkSet && !own.inkAuto {
			ink, inkSet = table.ink, table.inkSet
		}
	}
	behind := theme.White
	switch {
	case given.fill == 1:
		behind = given.fillColor
	case given.fill == 2:
		return false, false
	case own.fill == 1:
		behind = own.fillColor
	case own.fill == 2:
		// Not one colour: judged against a gradient's stops, if known.
		return inkSet && dml.LowOnMost(ink, 1, own.stops), true
	case table.fill == 1:
		behind = table.fillColor
	case table.fill == 2:
		return false, true
	}
	if !inkSet {
		return false, true // automatic: dark on light, light on dark
	}
	return gofastcore.LowContrast(ink.R, ink.G, ink.B, behind.R, behind.G, behind.B), true
}

// tableLook returns what the style of the styled table a cell is in
// gives it, if it is in one.
func (sh *sheet) tableLook(tables []declaredTable, row, col int32) look {
	for i := range tables {
		t := &tables[i]
		if row < t.at.r0 || row > t.at.r1 || col < t.at.c0 || col > t.at.c1 {
			continue
		}
		if ts := sh.wb.styles.tableStyle(t.style); ts != nil {
			return ts.at(t, row, col)
		}
	}
	return look{}
}
