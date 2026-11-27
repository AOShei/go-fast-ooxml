package wml

import (
	"github.com/AOShei/go-fast-core/label"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// Word numbers list items and numbered headings itself: the file stores a
// definition ("level 2 is %1.%2, decimal, starting at 1") and which
// paragraphs use it, never the label. This file turns those back into the
// label a reader sees ("4.2"), which is content: a contract refers to
// "clause 4.2", not to "the second sub-item of the fourth item".

const maxLevels = 9

// maxLabelText bounds the template of a list label.
const maxLabelText = warn.MaxListLabel

// level is the definition of one level of a list.
type level struct {
	start   int
	format  string // numFmt: decimal, lowerLetter, bullet, ...
	text    string // lvlText: "%1.%2." or a bullet character
	restart int    // lvlRestart: -1 default, 0 never, n: after level n
	legal   bool   // isLgl: show every level in decimal
	style   string // pStyle: the paragraph style this level belongs to
	font    string // the font of the label, which decides what a bullet is
	set     bool
}

type abstractNum struct {
	levels    [maxLevels]level
	styleLink string // this definition is the one a numbering style names
	numStyle  string // this definition defers to a numbering style
}

// num is an instance of a list: a definition plus overrides.
type num struct {
	abstract int
	override [maxLevels]struct {
		start    int
		hasStart bool
		level    level
	}
}

type numbering struct {
	abstracts map[int]*abstractNum
	nums      map[int]*num
	cut       int // labels longer than maxLabelText, cut there
}

// counter is the running state of one list definition.
type counter struct {
	value   [maxLevels]int
	started [maxLevels]bool
}

// numState is the numbering state of a document while it is read.
type numState struct {
	counters map[int]*counter         // by abstract definition
	applied  map[int]*[maxLevels]bool // by instance: start overrides already used
}

func readNumbering(s *xmlscan.Scanner) *numbering {
	n := &numbering{abstracts: map[int]*abstractNum{}, nums: map[int]*num{}}
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		switch {
		case s.Is(ns.Word, "abstractNum"):
			id, ok := intAttr(s, "abstractNumId")
			a := &abstractNum{}
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
					continue
				}
				switch {
				case s.Is(ns.Word, "lvl"):
					if i, ok := intAttr(s, "ilvl"); ok && i < maxLevels {
						n.cut += readLevel(s, &a.levels[i])
					} else {
						s.Skip()
					}
				case s.Is(ns.Word, "styleLink"):
					v, _ := s.Attr(ns.Word, "val")
					a.styleLink = string(v)
				case s.Is(ns.Word, "numStyleLink"):
					v, _ := s.Attr(ns.Word, "val")
					a.numStyle = string(v)
				default:
					s.Skip()
				}
			}
			if ok {
				n.abstracts[id] = a
			}
		case s.Is(ns.Word, "num"):
			id, ok := intAttr(s, "numId")
			inst := &num{abstract: -1}
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
					continue
				}
				switch {
				case s.Is(ns.Word, "abstractNumId"):
					if v, ok := intAttr(s, "val"); ok {
						inst.abstract = v
					}
				case s.Is(ns.Word, "lvlOverride"):
					i, okLevel := intAttr(s, "ilvl")
					inner := s.Depth()
					for s.Next() != xmlscan.EOF && s.Depth() >= inner {
						if s.Kind() != xmlscan.Start || s.Depth() != inner+1 || !okLevel || i >= maxLevels {
							continue
						}
						switch {
						case s.Is(ns.Word, "startOverride"):
							if v, ok := intAttr(s, "val"); ok {
								inst.override[i].start, inst.override[i].hasStart = v, true
							}
						case s.Is(ns.Word, "lvl"):
							n.cut += readLevel(s, &inst.override[i].level)
						default:
							s.Skip()
						}
					}
				default:
					s.Skip()
				}
			}
			if ok {
				n.nums[id] = inst
			}
		default:
			s.Skip()
		}
	}
	// A definition that defers to a numbering style uses the definition
	// that style is linked to.
	for id, a := range n.abstracts {
		if a.numStyle == "" {
			continue
		}
		for _, target := range n.abstracts {
			if target.styleLink == a.numStyle {
				n.abstracts[id] = target
				break
			}
		}
	}
	return n
}

func intAttr(s *xmlscan.Scanner, name string) (int, bool) {
	v, ok := s.Attr(ns.Word, name)
	if !ok {
		return 0, false
	}
	return atoi(v)
}

func readLevel(s *xmlscan.Scanner, l *level) (cut int) {
	*l = level{start: 1, format: "decimal", restart: -1, set: true}
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() == depth+2 && s.Is(ns.Word, "rFonts") {
			if v, ok := s.Attr(ns.Word, "ascii"); ok {
				l.font = string(v)
			}
			continue
		}
		if s.Depth() != depth+1 {
			continue
		}
		switch {
		case s.Is(ns.Word, "start"):
			if v, ok := intAttr(s, "val"); ok {
				l.start = v
			}
		case s.Is(ns.Word, "numFmt"):
			v, _ := s.Attr(ns.Word, "val")
			l.format = string(v)
		case s.Is(ns.Word, "lvlText"):
			// A label is a few characters. It is repeated on every item
			// of the list, so a long one is bounded here.
			// The cut does not split a character: the value is valid
			// UTF-8 from the scanner, and stays so.
			v, _ := s.Attr(ns.Word, "val")
			if len(v) > maxLabelText {
				n := maxLabelText
				for n > 0 && !utf8.RuneStart(v[n]) {
					n-- // do not cut a character in two
				}
				v = v[:n]
				cut = 1
			}
			l.text, _ = clean.String(v, clean.Ligatures)
		case s.Is(ns.Word, "lvlRestart"):
			if v, ok := intAttr(s, "val"); ok {
				l.restart = v
			}
		case s.Is(ns.Word, "isLgl"):
			v, has := s.Attr(ns.Word, "val")
			l.legal = !has || isOn(v)
		case s.Is(ns.Word, "pStyle"):
			v, _ := s.Attr(ns.Word, "val")
			l.style = string(v)
		case s.Is(ns.Word, "rPr"):
			// read on: the font of the label is inside
		default:
			s.Skip()
		}
	}
	return cut
}

// levelFor returns the definition of a level of a list instance, and the
// definition's id, or nil if the instance or level is not defined.
func (n *numbering) levelFor(numID, ilvl int) (*level, *num, int) {
	if n == nil || ilvl < 0 || ilvl >= maxLevels {
		return nil, nil, 0
	}
	inst := n.nums[numID]
	if inst == nil {
		return nil, nil, 0
	}
	if inst.override[ilvl].level.set {
		return &inst.override[ilvl].level, inst, inst.abstract
	}
	a := n.abstracts[inst.abstract]
	if a == nil || !a.levels[ilvl].set {
		return nil, nil, 0
	}
	return &a.levels[ilvl], inst, inst.abstract
}

// levelOfStyle returns the level of a list instance that is tied to a
// paragraph style, or -1. It is how a heading style linked to a multilevel
// list says which level it numbers.
func (n *numbering) levelOfStyle(numID int, styleID string) int {
	if n == nil || styleID == "" {
		return -1
	}
	inst := n.nums[numID]
	if inst == nil {
		return -1
	}
	a := n.abstracts[inst.abstract]
	if a == nil {
		return -1
	}
	for i := range a.levels {
		if a.levels[i].set && a.levels[i].style == styleID {
			return i
		}
	}
	return -1
}

// next advances the list for a paragraph at the given level and returns
// its label and whether the list is an ordered one.
//
// Counters belong to the definition, not to the instance: two instances of
// one definition continue each other's numbering unless the second
// overrides the start, which is how Word behaves.
func (st *numState) next(n *numbering, numID, ilvl int) (marker string, ordered, ok bool) {
	lv, inst, abstractID := n.levelFor(numID, ilvl)
	if lv == nil {
		return "", false, false
	}
	if st.counters == nil {
		st.counters = map[int]*counter{}
		st.applied = map[int]*[maxLevels]bool{}
	}
	c := st.counters[abstractID]
	if c == nil {
		c = &counter{}
		st.counters[abstractID] = c
	}
	used := st.applied[numID]
	if used == nil {
		used = &[maxLevels]bool{}
		st.applied[numID] = used
	}
	start := lv.start
	if o := &inst.override[ilvl]; o.hasStart && !used[ilvl] {
		start, c.started[ilvl] = o.start, false
	}
	used[ilvl] = true
	if c.started[ilvl] {
		c.value[ilvl]++
	} else {
		c.value[ilvl], c.started[ilvl] = start, true
	}
	// Deeper levels start again, unless their definition says otherwise.
	for k := ilvl + 1; k < maxLevels; k++ {
		deeper, _, _ := n.levelFor(numID, k)
		restart := -1
		if deeper != nil {
			restart = deeper.restart
		}
		if restart == 0 || (restart > 0 && ilvl > restart-1) {
			continue
		}
		c.started[k] = false
	}

	ordered = lv.format != "bullet" && lv.format != "none"
	if lv.format == "none" && lv.text == "" {
		return "", false, true
	}
	if lv.format == "bullet" {
		return label.Bullet(lv.text, lv.font), false, true
	}
	var sb strings.Builder
	for i := 0; i < len(lv.text); i++ {
		ch := lv.text[i]
		if ch != '%' || i+1 == len(lv.text) || lv.text[i+1] < '1' || lv.text[i+1] > '9' {
			sb.WriteByte(ch)
			continue
		}
		i++
		k := int(lv.text[i] - '1')
		ref, _, _ := n.levelFor(numID, k)
		if ref == nil {
			continue
		}
		v := c.value[k]
		if !c.started[k] {
			v = ref.start // a level not yet used shows its first value
		}
		format := ref.format
		if lv.legal && k != ilvl {
			format = "decimal"
		}
		sb.WriteString(formatNumber(v, format))
	}
	return strings.TrimSpace(sb.String()), ordered, true
}

// formatNumber writes a counter value in one of Word's number formats.
// A format this function does not know is written in decimal, which keeps
// the value even if not its look.
func formatNumber(v int, format string) string {
	switch format {
	case "none", "bullet":
		return ""
	case "decimalZero":
		if v >= 0 && v < 10 {
			return "0" + strconv.Itoa(v)
		}
	case "upperRoman":
		if r := label.Roman(v); r != "" {
			return r
		}
	case "lowerRoman":
		if r := label.Roman(v); r != "" {
			return strings.ToLower(r)
		}
	case "upperLetter":
		if l := label.Letters(v); l != "" {
			return l
		}
	case "lowerLetter":
		if l := label.Letters(v); l != "" {
			return strings.ToLower(l)
		}
	case "ordinal":
		if v > 0 {
			return strconv.Itoa(v) + ordinalSuffix(v)
		}
	case "cardinalText":
		if w := cardinal(v); w != "" {
			return strings.ToUpper(w[:1]) + w[1:]
		}
	case "ordinalText":
		if w := ordinalWord(v); w != "" {
			return strings.ToUpper(w[:1]) + w[1:]
		}
	case "decimalEnclosedCircle":
		if v >= 1 && v <= 20 {
			return string(rune(0x2460 + v - 1))
		}
	case "chicago":
		if v >= 1 && v <= 40 {
			marks := []string{"*", "†", "‡", "§"}
			return strings.Repeat(marks[(v-1)%4], (v-1)/4+1)
		}
	}
	return strconv.Itoa(v)
}

func ordinalSuffix(v int) string {
	if v%100 >= 11 && v%100 <= 13 {
		return "th"
	}
	switch v % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

var (
	ones    = []string{"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
	tens    = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}
	ordOnes = []string{"", "first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth", "eleventh", "twelfth", "thirteenth", "fourteenth", "fifteenth", "sixteenth", "seventeenth", "eighteenth", "nineteenth"}
	ordTens = []string{"", "", "twentieth", "thirtieth", "fortieth", "fiftieth", "sixtieth", "seventieth", "eightieth", "ninetieth"}
)

// cardinal and ordinalWord spell 1 to 99 in English, the only language
// they know; larger values fall back to digits.
func cardinal(v int) string {
	switch {
	case v < 1 || v > 99:
		return ""
	case v < 20:
		return ones[v]
	case v%10 == 0:
		return tens[v/10]
	}
	return tens[v/10] + "-" + ones[v%10]
}

func ordinalWord(v int) string {
	switch {
	case v < 1 || v > 99:
		return ""
	case v < 20:
		return ordOnes[v]
	case v%10 == 0:
		return ordTens[v/10]
	}
	return tens[v/10] + "-" + ordOnes[v%10]
}
