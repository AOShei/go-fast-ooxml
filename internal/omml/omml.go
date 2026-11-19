// Package omml writes Office Math (OMML), the equations of Word and
// PowerPoint, in UnicodeMath: the linear form Word itself shows when an
// equation is switched to "linear" (Unicode Technical Note 28). A fraction
// is a/b, an exponent x^2, a square root √x, a sum ∑_(i=1)^n▒x_i. Every
// character the equation holds is kept, in order; what is added are the
// operators and brackets that say how the characters are laid out.
//
// Where a part of a structure is more than one symbol it is put in
// parentheses, which UnicodeMath takes away again when it lays the
// equation out: x^(n+1), (a+b)/c.
package omml

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Linear appends the linear form of the math element whose start tag is
// current (m:oMathPara, m:oMath, or any part of one) to dst, and reads the
// scanner to the element's end. The equations of an m:oMathPara are on
// lines of their own.
func Linear(s *xmlscan.Scanner, dst []byte) []byte {
	w := writer{s: s}
	return append(dst, w.element()...)
}

type writer struct {
	s *xmlscan.Scanner
}

// element returns the linear form of the element whose start tag is
// current, having read to its end.
func (w *writer) element() string {
	s := w.s
	switch s.Space() {
	case ns.Math:
		switch string(s.Local()) {
		case "oMathPara":
			return w.lines()
		case "r":
			return w.run()
		case "t":
			return string(s.AppendText(nil))
		case "f":
			return w.fraction()
		case "sSup", "sSub", "sSubSup", "sPre":
			return w.script(string(s.Local()))
		case "rad":
			return w.radical()
		case "nary":
			return w.nary()
		case "d":
			return w.delimiter()
		case "func":
			p := w.parts()
			return p.one("fName") + "\u2061" + group(p.one("e"))
		case "acc":
			p := w.parts()
			return base(p.one("e")) + p.prop("chr", "\u0302")
		case "bar":
			p := w.parts()
			if p.prop("pos", "bot") == "top" {
				return "¯" + group(p.one("e"))
			}
			return "▁" + group(p.one("e"))
		case "box":
			return "□" + group(w.parts().one("e"))
		case "borderBox":
			return "▭" + group(w.parts().one("e"))
		case "groupChr":
			p := w.parts()
			return p.prop("chr", "⏟") + group(p.one("e"))
		case "limLow":
			p := w.parts()
			return name(p.one("e")) + "┬" + group(p.one("lim"))
		case "limUpp":
			p := w.parts()
			return name(p.one("e")) + "┴" + group(p.one("lim"))
		case "m":
			return w.matrix()
		case "eqArr":
			return "█(" + strings.Join(w.parts().all("e"), "@") + ")"
		}
		if strings.HasSuffix(string(s.Local()), "Pr") {
			s.Skip()
			return ""
		}
		// m:oMath, m:e, m:num, m:phant and the like: their content.
		return w.content()
	case ns.Word:
		switch string(s.Local()) {
		case "t":
			return string(s.AppendText(nil))
		case "r", "ins", "sdt", "sdtContent", "smartTag", "customXml", "hyperlink":
			return w.content()
		}
	case ns.MC:
		return w.content()
	}
	s.Skip()
	return ""
}

// content returns the linear form of the children of the current element.
// Where a structure (a script, a fraction) is followed by a letter, a
// digit or a bracket, a space ends it, as Word writes it: x^k a^n, not
// x^ka^n, which is x to the power ka.
func (w *writer) content() string {
	s := w.s
	var sb strings.Builder
	structure := false
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return sb.String()
		}
		if k != xmlscan.Start {
			continue
		}
		run := s.Is(ns.Math, "r") || s.Is(ns.Math, "t") || s.Space() == ns.Word
		delimited := s.Is(ns.Math, "d")
		piece := w.element()
		if piece == "" {
			continue
		}
		if structure && joins(piece) {
			sb.WriteByte(' ')
		}
		sb.WriteString(piece)
		structure = !run && !delimited
	}
}

// joins reports whether text that starts so would be read as part of a
// structure before it.
func joins(piece string) bool {
	r, _ := utf8.DecodeRuneInString(piece)
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '(' || r == '[' || r == '{'
}

// lines returns the equations of m:oMathPara, one to a line.
func (w *writer) lines() string {
	s := w.s
	var lines []string
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return strings.Join(lines, "\n")
		}
		if k != xmlscan.Start {
			continue
		}
		if s.Is(ns.Math, "oMath") {
			lines = append(lines, w.content())
		} else if line := w.element(); line != "" {
			lines = append(lines, line)
		}
	}
}

// run returns the text of m:r. Text marked as ordinary text, not math, is
// quoted, as UnicodeMath writes it.
func (w *writer) run() string {
	s := w.s
	var sb strings.Builder
	normal := false
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			break
		}
		if k != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Math, "rPr"):
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() == xmlscan.Start && s.Is(ns.Math, "nor") {
					v, ok := s.Attr(ns.Math, "val")
					normal = !ok || on(v)
				}
			}
		case s.Is(ns.Math, "t"), s.Is(ns.Word, "t"):
			sb.Write(s.AppendText(nil))
		case s.Is(ns.Word, "tab"):
			sb.WriteByte('\t')
		case s.Is(ns.Word, "br"):
			sb.WriteByte('\n')
		default:
			s.Skip()
		}
	}
	if normal && sb.Len() > 0 {
		return `"` + sb.String() + `"`
	}
	return sb.String()
}

func on(v []byte) bool {
	switch string(v) {
	case "0", "off", "false":
		return false
	}
	return true
}

// structure is what a structure's children hold: its arguments, by the
// name of their element, in order, and its properties, by the name of
// the property's element.
type structure struct {
	args  []arg
	props map[string]string
}

type arg struct {
	name, text string
}

// parts reads the children of the current structure.
func (w *writer) parts() structure {
	s := w.s
	var p structure
	depth := s.Depth()
	for {
		k := s.Next()
		if k == xmlscan.EOF || k == xmlscan.End && s.Depth() < depth {
			return p
		}
		if k != xmlscan.Start {
			continue
		}
		name := string(s.Local())
		switch {
		case s.Space() != ns.Math:
			s.Skip()
		case strings.HasSuffix(name, "Pr"):
			p.props = w.props(p.props)
		case name == "mr":
			// A row of a matrix: its cells.
			row := w.parts()
			p.args = append(p.args, arg{name: "mr", text: strings.Join(row.all("e"), "&")})
		default:
			p.args = append(p.args, arg{name: name, text: w.content()})
		}
	}
}

// props reads the properties of a structure: the m:val of each child.
func (w *writer) props(into map[string]string) map[string]string {
	s := w.s
	if into == nil {
		into = map[string]string{}
	}
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || s.Space() != ns.Math {
			continue
		}
		name := string(s.Local())
		if v, ok := s.Attr(ns.Math, "val"); ok {
			into[name] = string(v)
		} else if strings.HasSuffix(name, "Hide") {
			into[name] = "1" // a switch with no value is on
		}
	}
	return into
}

// one returns the first argument of that name.
func (p structure) one(name string) string {
	for _, a := range p.args {
		if a.name == name {
			return a.text
		}
	}
	return ""
}

// all returns every argument of that name.
func (p structure) all(name string) []string {
	var out []string
	for _, a := range p.args {
		if a.name == name {
			out = append(out, a.text)
		}
	}
	return out
}

// prop returns a property, or its default if it is not set. An empty
// value is a value: a delimiter with no opening character.
func (p structure) prop(name, def string) string {
	if v, ok := p.props[name]; ok {
		return v
	}
	return def
}

// flag reports whether a switch is on.
func (p structure) flag(name string) bool {
	v, ok := p.props[name]
	return ok && on([]byte(v))
}

func (w *writer) fraction() string {
	p := w.parts()
	num, den := group(p.one("num")), group(p.one("den"))
	switch p.prop("type", "bar") {
	case "noBar":
		return num + "¦" + den
	case "lin":
		return num + "∕" + den
	case "skw":
		return num + "⊘" + den
	}
	return num + "/" + den
}

func (w *writer) script(kind string) string {
	p := w.parts()
	// An empty script is left out, with its operator.
	scripts := ""
	if sub := p.one("sub"); sub != "" && kind != "sSup" {
		scripts += "_" + group(sub)
	}
	if sup := p.one("sup"); sup != "" && kind != "sSub" {
		scripts += "^" + group(sup)
	}
	e := p.one("e")
	if scripts == "" {
		return e
	}
	if kind == "sPre" {
		// Scripts before the base.
		return scripts + base(e)
	}
	return base(e) + scripts
}

func (w *writer) radical() string {
	p := w.parts()
	e := group(p.one("e"))
	deg := p.one("deg")
	switch {
	case p.flag("degHide") || deg == "":
		return "√" + e
	case deg == "3":
		return "∛" + e
	case deg == "4":
		return "∜" + e
	}
	return "√(" + deg + "&" + p.one("e") + ")"
}

func (w *writer) nary() string {
	p := w.parts()
	out := p.prop("chr", "∫")
	if sub := p.one("sub"); sub != "" && !p.flag("subHide") {
		out += "_" + group(sub)
	}
	if sup := p.one("sup"); sup != "" && !p.flag("supHide") {
		out += "^" + group(sup)
	}
	return out + "▒" + group(p.one("e"))
}

func (w *writer) delimiter() string {
	p := w.parts()
	return p.prop("begChr", "(") + strings.Join(p.all("e"), p.prop("sepChr", "│")) + p.prop("endChr", ")")
}

func (w *writer) matrix() string {
	return "■(" + strings.Join(w.parts().all("mr"), "@") + ")"
}

// group returns an argument as it is written after an operator: as it
// is if it is one symbol, a number or already in brackets, else in
// parentheses.
func group(x string) string {
	if single(x) {
		return x
	}
	return "(" + x + ")"
}

// base is group for what an operator follows.
func base(x string) string { return group(x) }

// name is base for what may be the name of a function (lim, max), which
// UnicodeMath reads as one thing.
func name(x string) string {
	for _, r := range x {
		if !unicode.IsLetter(r) {
			return group(x)
		}
	}
	return x
}

// single reports whether x reads as one thing after an operator.
func single(x string) bool {
	if x == "" || bracketed(x) {
		return true
	}
	symbols, digits := 0, true
	for _, r := range x {
		if unicode.Is(unicode.Mn, r) {
			continue // a combining mark goes with what it follows
		}
		symbols++
		if !unicode.IsDigit(r) && r != '.' {
			digits = false
		}
	}
	return symbols == 1 || digits
}

// bracketed reports whether x is wholly inside one pair of brackets.
func bracketed(x string) bool {
	open, size := utf8.DecodeRuneInString(x)
	closer, ok := pairs[open]
	if !ok {
		return false
	}
	last, lastSize := utf8.DecodeLastRuneInString(x)
	if last != closer || len(x) == size {
		return false
	}
	// The opening bracket must close at the very end.
	depth := 0
	inner := x[:len(x)-lastSize]
	for _, r := range inner {
		switch r {
		case open:
			depth++
		case closer:
			depth--
			if depth == 0 {
				return false
			}
		}
	}
	return depth == 1
}

var pairs = map[rune]rune{'(': ')', '[': ']', '{': '}', '〖': '〗', '⟨': '⟩', '|': '|', '‖': '‖'}
