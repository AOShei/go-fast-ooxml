package wml

import (
	"strings"

	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// role is what a paragraph style says about the paragraphs that use it,
// beyond their heading level and numbering.
type role uint8

const (
	roleNone role = iota
	roleTitle
	roleSubtitle
	roleQuote
	roleCode
)

// style is a paragraph style with what it inherits already folded in.
type style struct {
	name    string
	based   string
	outline int8 // outline level 0-8, or -1
	numID   int  // numbering instance, 0 for "none", -1 for not set
	ilvl    int8 // numbering level, or -1
	role    role
	// nested is the list level, from 2, that the style's name states:
	// Word's built-in "List Bullet 2" to "List Number 5" each use a list
	// of their own at its first level and show the nesting by indent.
	nested uint8
	// run is the character formatting the style gives its paragraphs'
	// text, with everything it inherits and the document defaults folded
	// in.
	run runProps
	// own is the on/off properties the style switches on itself or by
	// inheritance from other styles, leaving out the document defaults.
	own  uint8
	done bool // inheritance has been resolved
}

// charStyle is a character style: formatting for a run of text.
type charStyle struct {
	run   runProps
	based string
	done  bool
}

// styles holds the styles of a document by style id.
type styles struct {
	byID  map[string]*style
	chars map[string]*charStyle
	// def is the default paragraph style, used by paragraphs that name none.
	def *style
	// defaults is the character formatting of text that no style formats.
	defaults runProps
}

// char returns the formatting of a character style, or nil.
func (st *styles) char(id string) *runProps {
	if st == nil || id == "" {
		return nil
	}
	if c := st.chars[id]; c != nil {
		return &c.run
	}
	return nil
}

func (st *styles) get(id []byte) *style {
	if st == nil {
		return nil
	}
	if len(id) == 0 {
		return st.def
	}
	return st.byID[string(id)]
}

// readStyles reads the paragraph and character styles and the document
// defaults from a styles part. Table styles are skipped: the formatting
// they give the text of a table is not taken into account.
func readStyles(s *xmlscan.Scanner) *styles {
	st := &styles{byID: map[string]*style{}, chars: map[string]*charStyle{}}
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		if s.Is(ns.Word, "docDefaults") {
			depth := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				// w:docDefaults / w:rPrDefault / w:rPr
				if s.Kind() == xmlscan.Start && s.Depth() == depth+2 && s.Is(ns.Word, "rPr") {
					readRunProps(s, &st.defaults)
				}
			}
			continue
		}
		if !s.Is(ns.Word, "style") {
			s.Skip()
			continue
		}
		typ, _ := s.Attr(ns.Word, "type")
		paragraph, character := string(typ) == "paragraph", string(typ) == "character"
		if !paragraph && !character {
			s.Skip()
			continue
		}
		id, _ := s.Attr(ns.Word, "styleId")
		def, _ := s.Attr(ns.Word, "default")
		cur := &style{outline: -1, numID: -1, ilvl: -1}
		depth := s.Depth()
		for s.Next() != xmlscan.EOF && s.Depth() >= depth {
			if s.Kind() != xmlscan.Start {
				continue
			}
			switch {
			case s.Depth() == depth+1 && s.Is(ns.Word, "name"):
				v, _ := s.Attr(ns.Word, "val")
				cur.name = string(v)
			case s.Depth() == depth+1 && s.Is(ns.Word, "basedOn"):
				v, _ := s.Attr(ns.Word, "val")
				cur.based = string(v)
			case s.Depth() == depth+1 && s.Is(ns.Word, "pPr") && paragraph:
				var pp paraProps
				readParaProps(s, &pp, nil)
				cur.outline, cur.numID, cur.ilvl = pp.outline, pp.numID, pp.ilvl
			case s.Depth() == depth+1 && s.Is(ns.Word, "rPr"):
				readRunProps(s, &cur.run)
			case s.Depth() == depth+1:
				s.Skip()
			}
		}
		if len(id) == 0 {
			continue
		}
		if character {
			st.chars[string(id)] = &charStyle{run: cur.run, based: cur.based}
			continue
		}
		st.byID[string(id)] = cur
		if isOn(def) && st.def == nil {
			st.def = cur
		}
	}
	for _, cur := range st.byID {
		st.resolve(cur, 0)
	}
	for _, cur := range st.chars {
		st.resolveChar(cur, 0)
	}
	// What no style sets comes from the document defaults.
	for _, cur := range st.byID {
		cur.own = cur.run.on
		cur.run.under(&st.defaults)
	}
	return st
}

// resolveChar folds the formatting a character style inherits into it.
func (st *styles) resolveChar(cur *charStyle, depth int) {
	if cur.done {
		return
	}
	cur.done = true
	parent := st.chars[cur.based]
	if parent == nil || depth > 32 {
		return
	}
	st.resolveChar(parent, depth+1)
	cur.run.under(&parent.run)
}

// resolve folds the properties a style inherits into it. A style whose
// chain of parents loops back on itself, or is absurdly long, inherits
// nothing beyond that point.
func (st *styles) resolve(cur *style, depth int) {
	if cur.done {
		return
	}
	cur.done = true
	cur.role, cur.outline = classify(cur.name, cur.outline)
	cur.nested = nestedList(cur.name)
	parent := st.byID[cur.based]
	if parent == nil || depth > 32 {
		return
	}
	st.resolve(parent, depth+1)
	if cur.outline < 0 {
		cur.outline = parent.outline
	}
	if cur.numID < 0 {
		cur.numID = parent.numID
		if cur.ilvl < 0 {
			cur.ilvl = parent.ilvl
		}
	}
	if cur.role == roleNone {
		cur.role = parent.role
	}
	cur.run.under(&parent.run)
}

// classify reads what a style's name says. Word stores the names of its
// built-in styles in English whatever the language of the interface, so
// "heading 2" is a second-level heading in every Word file; LibreOffice
// writes the same names.
func classify(name string, outline int8) (role, int8) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "title":
		return roleTitle, outline
	case "subtitle":
		return roleSubtitle, outline
	case "quote", "intense quote", "quotations", "block text":
		return roleQuote, outline
	case "html preformatted", "preformatted text", "source code", "code", "macro text", "plain text":
		return roleCode, outline
	}
	if outline < 0 && strings.HasPrefix(n, "heading ") && len(n) == len("heading ")+1 {
		if d := n[len(n)-1]; d >= '1' && d <= '9' {
			return roleNone, int8(d - '1')
		}
	}
	return roleNone, outline
}

// nestedList returns the list level that one of Word's built-in list style
// names states ("List Bullet 3" is 3), or 0.
func nestedList(name string) uint8 {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) < 6 || !strings.HasPrefix(n, "list") {
		return 0
	}
	d := n[len(n)-1]
	if d < '2' || d > '5' || n[len(n)-2] != ' ' {
		return 0
	}
	switch strings.TrimSpace(n[4 : len(n)-2]) {
	case "", "bullet", "number", "continue":
		return d - '0'
	}
	return 0
}

// isOn reads a Word on/off value that is present: "1", "true" or "on".
func isOn(v []byte) bool {
	switch string(v) {
	case "1", "true", "on":
		return true
	}
	return false
}

// atoi reads a small non-negative decimal number and reports whether the
// bytes were one. It saturates, so a hostile value cannot overflow.
func atoi(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		if n < 1<<24 {
			n = n*10 + int(c-'0')
		}
	}
	return n, true
}
