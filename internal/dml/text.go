package dml

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/hidden"
	"github.com/AOShei/go-fast-core/label"
	"github.com/AOShei/go-fast-core/symbol"
	"github.com/AOShei/go-fast-core/xmlscan"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/omml"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
)

// RunProps is the formatting of a run of text, as far as this library
// reads it. A zero field says nothing, and what holds is then what the
// next layer down says: see Text.Chain.
type RunProps struct {
	Size    int32            // in hundredths of a point
	On      gofastcore.Style // bold, italic and the like that are on
	Said    gofastcore.Style // those the properties speak of, on or off
	Ink     Fill             // what the letters are filled with
	Outline Paint            // PaintOther: the letters have a drawn outline
	// Font is the symbol font of the run's letters (a:latin), Sym that of
	// its symbols (a:sym), each meant only if its Said flag is set.
	Font, Sym         symbol.Font
	FontSaid, SymSaid bool
}

// inherit fills in what r does not say from the layer below it.
func (r *RunProps) inherit(from *RunProps) {
	if r.Size == 0 {
		r.Size = from.Size
	}
	r.On |= from.On &^ r.Said
	r.Said |= from.Said
	if r.Ink.Paint == PaintUnset {
		r.Ink = from.Ink
	}
	if r.Outline == PaintUnset {
		r.Outline = from.Outline
	}
	if !r.FontSaid {
		r.Font, r.FontSaid = from.Font, from.FontSaid
	}
	if !r.SymSaid {
		r.Sym, r.SymSaid = from.Sym, from.SymSaid
	}
}

// BulletKind is what stands in front of a paragraph.
type BulletKind uint8

const (
	BulletUnset   BulletKind = iota // nothing is said
	BulletNone                      // no bullet
	BulletChar                      // a character
	BulletNumber                    // a number that counts up
	BulletPicture                   // a picture
)

// ParaProps is the formatting of paragraphs at one level of a list
// style, or of one paragraph.
type ParaProps struct {
	Bullet   BulletKind
	Char     string // the bullet character
	Scheme   string // how a number is written: arabicPeriod, romanUcParenR
	Start    int32  // the first number, if said
	Font     string // the bullet's font
	FontSaid bool
	Run      RunProps
}

// maxLevels is the number of levels a list style has.
const maxLevels = 9

// ListStyle is the formatting of each level of the text in a shape. A
// shape has one; so has the placeholder it fills in on the layout and on
// the master, and the master and the presentation have more for whole
// kinds of text.
type ListStyle [maxLevels]ParaProps

// ReadListStyle reads a list style from the element whose Start token is
// current (a:lstStyle, p:titleStyle, p:defaultTextStyle) and consumes
// it. It returns nil for one that says nothing.
func ReadListStyle(s *xmlscan.Scanner) *ListStyle {
	var ls *ListStyle
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		name := s.Local()
		if s.Space() == ns.Draw && len(name) == 7 && string(name[:3]) == "lvl" && string(name[4:]) == "pPr" && name[3] >= '1' && name[3] <= '9' {
			if ls == nil {
				ls = new(ListStyle)
			}
			readParaProps(s, &ls[name[3]-'1'])
			continue
		}
		s.Skip()
	}
	return ls
}

// readParaProps reads paragraph properties (a:pPr, a:lvl1pPr) and
// consumes the element.
func readParaProps(s *xmlscan.Scanner, pp *ParaProps) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Space() != ns.Draw {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "buNone":
			pp.Bullet = BulletNone
		case "buChar":
			pp.Bullet = BulletChar
			v, _ := s.Attr(xmlscan.NoSpace, "char")
			pp.Char = short(v, pp.Char)
		case "buAutoNum":
			pp.Bullet = BulletNumber
			v, _ := s.Attr(xmlscan.NoSpace, "type")
			pp.Scheme = short(v, pp.Scheme)
			pp.Start = 0
			if n, ok := intAttr(s, "startAt"); ok && n >= 1 && n <= 32767 {
				pp.Start = int32(n)
			}
		case "buBlip":
			pp.Bullet = BulletPicture
		case "buFont":
			v, _ := s.Attr(xmlscan.NoSpace, "typeface")
			pp.Font, pp.FontSaid = short(v, pp.Font), true
		case "buFontTx":
			pp.Font, pp.FontSaid = "", true // the font of the text
		case "defRPr":
			readRunProps(s, &pp.Run, nil)
			continue
		}
		s.Skip()
	}
}

// short returns v as a string of bounded length, reusing was if it says
// the same: the same few names recur in every paragraph.
func short(v []byte, was string) string {
	const limit = 32
	if len(v) > limit {
		n := limit
		for n > 0 && !utf8.RuneStart(v[n]) {
			n-- // do not cut a character in two
		}
		v = v[:n]
	}
	if string(v) == was {
		return was
	}
	return string(v)
}

// link is what a run's properties say it links to.
type link struct {
	id      []byte // the relationship
	program bool   // the link starts a program or runs a macro
}

// readRunProps reads run properties (a:rPr, a:defRPr) and consumes the
// element.
func readRunProps(s *xmlscan.Scanner, rp *RunProps, to *link) {
	if n, ok := intAttr(s, "sz"); ok && n > 0 && n <= 400000 {
		rp.Size = int32(n)
	}
	flag := func(name string, style gofastcore.Style, off string) {
		v, ok := s.Attr(xmlscan.NoSpace, name)
		if !ok {
			return
		}
		rp.Said |= style
		rp.On &^= style
		if string(v) != off && string(v) != "0" && string(v) != "false" {
			rp.On |= style
		}
	}
	flag("b", gofastcore.StyleBold, "0")
	flag("i", gofastcore.StyleItalic, "0")
	flag("u", gofastcore.StyleUnderline, "none")
	flag("strike", gofastcore.StyleStrike, "noStrike")
	if n, ok := percentAttr(s, "baseline"); ok {
		rp.Said |= gofastcore.StyleSub | gofastcore.StyleSuper
		rp.On &^= gofastcore.StyleSub | gofastcore.StyleSuper
		switch {
		case n > 0:
			rp.On |= gofastcore.StyleSuper
		case n < 0:
			rp.On |= gofastcore.StyleSub
		}
	}
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if f, ok := ReadFill(s); ok {
			rp.Ink = f
			continue
		}
		switch {
		case s.Is(ns.Draw, "ln"):
			// The outline of the letters: drawn if it has any fill but
			// none.
			rp.Outline = PaintNone
			inner := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= inner {
				if s.Kind() != xmlscan.Start {
					continue
				}
				if f, ok := ReadFill(s); ok {
					if f.Paint != PaintNone {
						rp.Outline = PaintOther
					}
					continue
				}
				s.Skip()
			}
			continue
		case s.Is(ns.Draw, "latin"):
			// A theme font ("+mn-lt") is never a symbol font.
			v, _ := s.Attr(xmlscan.NoSpace, "typeface")
			rp.Font, rp.FontSaid = symbol.ByName(string(v)), true
		case s.Is(ns.Draw, "sym"):
			v, _ := s.Attr(xmlscan.NoSpace, "typeface")
			rp.Sym, rp.SymSaid = symbol.ByName(string(v)), true
		case to != nil && (s.Is(ns.Draw, "hlinkClick") || s.Is(ns.Draw, "hlinkMouseOver")):
			if action, ok := s.Attr(xmlscan.NoSpace, "action"); ok && StartsProgram(action) {
				to.program = true
			}
			if id, ok := s.Attr(ns.Rel, "id"); ok && len(id) > 0 && s.Is(ns.Draw, "hlinkClick") {
				to.id = append(to.id[:0], id...)
			}
		}
		s.Skip()
	}
}

// StartsProgram reports whether the action of a link is one that starts a
// program or runs a macro, as opposed to going somewhere.
func StartsProgram(action []byte) bool {
	const program, macro = "ppaction://program", "ppaction://macro"
	return len(action) >= len(program) && string(action[:len(program)]) == program ||
		len(action) >= len(macro) && string(action[:len(macro)]) == macro
}

// Para is one paragraph of a text body that has text.
type Para struct {
	Start, End int                // its text, trimmed, in Text.Buf
	Spans      []gofastcore.Span  // formatting, links, and ranges the file declares not shown
	Display    gofastcore.Display // every reason the file declares for part of it
	Level      int                // from 0
	Listed     bool               // it has a bullet or a number
	Ordered    bool               // a number
	Marker     string             // the bullet or number as shown
	Size       float64            // the size its first text is shown at, in points
}

// defaultSize is the size of text that nothing gives a size, in
// hundredths of a point.
const defaultSize = 1800

// Text reads the text bodies of shapes. One Text is used for many bodies
// in turn, and keeps its buffers; it is not for use by two goroutines at
// once. The fields down to SlideNumber are set by the caller before each
// Read, and the rest are the result.
type Text struct {
	// Budget is what the document may still hold of text. Every
	// paragraph read is taken from it, and when it runs out the scan is
	// ended with warn.ErrText. Nil for no limit.
	Budget *gofastcore.TextBudget
	// Taken counts what was taken from Budget, for a caller that reads
	// a part again and gives it back first.
	Taken int64
	// Cost is what a paragraph takes from Budget beyond its text:
	// gofastcore.BlockCost where each paragraph becomes a block, zero
	// where the text goes into a table cell, which is charged with the
	// table.
	Cost int
	// Palette resolves colours.
	Palette *Palette
	// Chain is the list styles the body's text inherits from, nearest
	// first, after the body's own. Base is what holds where none of them
	// says anything.
	Chain []*ListStyle
	Base  RunProps
	// Styles asks for spans of formatting and links, and Link turns the
	// relationship of a link into its address.
	Styles bool
	Link   func(id []byte) string
	// SlideNumber, if not empty, is what a slide-number field shows.
	SlideNumber string

	Buf   []byte  // the text of the paragraphs
	Paras []Para  // the paragraphs that have text
	Scale float64 // the share of its size the text is shown at (autofit)

	Removed  int // control characters removed, over all bodies
	Unmapped int // codes of symbol fonts with no character, over all bodies
	Programs int // links that start a program or run a macro, over all bodies

	spans  []gofastcore.Span
	raw    []byte
	sym    []byte // for translating text in a symbol font
	own    *ListStyle
	levels [maxLevels]RunProps
	have   [maxLevels]bool
	to     link

	// The counters of numbered paragraphs.
	count  [maxLevels]int32
	scheme [maxLevels]string
	first  [maxLevels]int32
}

// Read reads the text body whose Start token is current (p:txBody,
// xdr:txBody, a:txBody) and consumes it.
func (t *Text) Read(s *xmlscan.Scanner) {
	t.Buf, t.Paras, t.spans = t.Buf[:0], t.Paras[:0], t.spans[:0]
	t.Scale = 1
	t.own = nil
	t.have = [maxLevels]bool{}
	t.scheme = [maxLevels]string{}
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Space() != ns.Draw || s.Depth() != depth+1 {
			s.Skip()
			continue
		}
		switch string(s.Local()) {
		case "bodyPr":
			t.bodyProps(s)
		case "lstStyle":
			t.own = ReadListStyle(s)
		case "p":
			t.para(s)
		default:
			s.Skip()
		}
	}
}

// Own returns the list style of the body last read, or nil if it has
// none. The style is the caller's to keep.
func (t *Text) Own() *ListStyle { return t.own }

// bodyProps reads what a:bodyPr says about fitting the text.
func (t *Text) bodyProps(s *xmlscan.Scanner) {
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Is(ns.Draw, "normAutofit") {
			// The text is shrunk to fit, to this share of its size.
			if n, ok := percentAttr(s, "fontScale"); ok && n > 0 && n <= 100000 {
				t.Scale = float64(n) / 100000
			}
		}
		s.Skip()
	}
}

// symbolFonts returns the symbol fonts that apply to a run's letters and
// to its symbols, from its own properties or the level of the list it is
// in. A symbol with no font of its own is in the letters' font.
func (t *Text) symbolFonts(rp *RunProps, level int) (font, sym symbol.Font) {
	r := *rp
	r.inherit(t.level(level))
	font, sym = r.Font, r.Sym
	if !r.SymSaid {
		sym = font
	}
	return font, sym
}

// level returns what a run at the given level inherits.
func (t *Text) level(l int) *RunProps {
	if !t.have[l] {
		var rp RunProps
		if t.own != nil {
			rp = t.own[l].Run
		}
		for _, ls := range t.Chain {
			if ls != nil {
				rp.inherit(&ls[l].Run)
			}
		}
		rp.inherit(&t.Base)
		t.levels[l], t.have[l] = rp, true
	}
	return &t.levels[l]
}

// para reads one paragraph (a:p).
func (t *Text) para(s *xmlscan.Scanner) {
	start, spanStart := len(t.Buf), len(t.spans)
	var pp ParaProps
	level := 0
	size := 0.0
	choice := -1
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Draw, "pPr"):
			if n, ok := intAttr(s, "lvl"); ok && n > 0 {
				level = int(min(n, maxLevels-1))
			}
			readParaProps(s, &pp)
		case s.Is(ns.Draw, "r"):
			if shown := t.run(s, start, level, false); size == 0 {
				size = shown
			}
		case s.Is(ns.Draw, "fld"):
			typ, _ := s.Attr(xmlscan.NoSpace, "type")
			if shown := t.run(s, start, level, string(typ) == "slidenum"); size == 0 {
				size = shown
			}
		case s.Is(ns.Draw, "br"):
			t.Buf = append(t.Buf, '\n')
			s.Skip()
		case s.Is(ns.Draw, "endParaRPr"):
			s.Skip()
		case s.Is(ns.MC, "Choice"):
			// One of two forms of the same content, for newer and older
			// programs: an equation, and a picture or plain text of it.
			choice = len(t.Buf)
		case s.Is(ns.MC, "Fallback"):
			if choice >= 0 && len(t.Buf) > choice {
				s.Skip()
			}
			choice = -1
		case s.Is(ns.Math, "oMathPara"), s.Is(ns.Math, "oMath"):
			// An equation, in UnicodeMath, the linear form Office shows.
			t.raw = omml.Linear(s, t.raw[:0])
			var removed int
			t.Buf, removed = clean.Append(t.Buf, t.raw, clean.Ligatures)
			t.Removed += removed
		}
		// Anything else is a container whose content is read as it comes.
	}

	// Trimmed, with the spans moved to match.
	raw := t.Buf[start:]
	from, to := 0, len(raw)
	for to > 0 {
		r, n := utf8.DecodeLastRune(raw[:to])
		if !unicode.IsSpace(r) {
			break
		}
		to -= n
	}
	for from < to {
		r, n := utf8.DecodeRune(raw[from:to])
		if !unicode.IsSpace(r) {
			break
		}
		from += n
	}
	if from == to {
		// An empty paragraph is spacing; it has no bullet and is not
		// counted.
		t.Buf, t.spans = t.Buf[:start], t.spans[:spanStart]
		return
	}
	// A paragraph the document has no room for ends the reading of the
	// part it is in; what was read before it is kept.
	if !t.Budget.Take(t.Cost + to - from) {
		t.Buf, t.spans = t.Buf[:start], t.spans[:spanStart]
		s.Abort(warn.ErrText)
		return
	}
	t.Taken += int64(t.Cost + to - from)
	p := Para{Start: start + from, End: start + to, Level: level, Size: size}
	p.Spans, p.Display = hidden.Settle(t.spans[spanStart:], from, to, raw)
	t.spans = t.spans[:spanStart]
	t.bullet(&p, &pp)
	t.Paras = append(t.Paras, p)
}

// bullet works out what stands in front of a paragraph.
func (t *Text) bullet(p *Para, pp *ParaProps) {
	l := p.Level
	bullet, font, fontSaid := pp, pp.Font, pp.FontSaid
	layer := func(from *ParaProps) {
		if bullet.Bullet == BulletUnset && from.Bullet != BulletUnset {
			bullet = from
		}
		if !fontSaid && from.FontSaid {
			font, fontSaid = from.Font, true
		}
	}
	if t.own != nil {
		layer(&t.own[l])
	}
	for _, ls := range t.Chain {
		if ls != nil {
			layer(&ls[l])
		}
	}
	// A paragraph ends the counting of the levels below it, and of its
	// own unless it carries the count on.
	for k := l + 1; k < maxLevels; k++ {
		t.scheme[k] = ""
	}
	switch bullet.Bullet {
	case BulletChar:
		p.Listed, p.Marker = true, label.Bullet(bullet.Char, font)
	case BulletPicture:
		p.Listed, p.Marker = true, "•"
	case BulletNumber:
		first := max(bullet.Start, 1)
		scheme := bullet.Scheme
		if scheme == "" {
			scheme = "arabicPeriod"
		}
		if t.scheme[l] == scheme && t.first[l] == first && t.count[l] < 1<<30 {
			t.count[l]++
		} else {
			t.scheme[l], t.first[l], t.count[l] = scheme, first, first
		}
		p.Listed, p.Ordered, p.Marker = true, true, Number(int(t.count[l]), scheme)
		return
	}
	t.scheme[l] = ""
}

// Number writes the number of a list item the way an automatic numbering
// scheme of DrawingML shows it: "arabicPeriod" gives "3.", "alphaLcParenR"
// gives "c)", "romanUcParenBoth" gives "(III)". A scheme this function
// does not know is written in digits with a full stop, which keeps the
// value if not its look.
func Number(n int, scheme string) string {
	body, rest := "", ""
	switch {
	case strings.HasPrefix(scheme, "alphaLc"):
		body, rest = strings.ToLower(label.Letters(n)), scheme[7:]
	case strings.HasPrefix(scheme, "alphaUc"):
		body, rest = label.Letters(n), scheme[7:]
	case strings.HasPrefix(scheme, "romanLc"):
		body, rest = strings.ToLower(label.Roman(n)), scheme[7:]
	case strings.HasPrefix(scheme, "romanUc"):
		body, rest = label.Roman(n), scheme[7:]
	case strings.HasPrefix(scheme, "circleNum"):
		if n >= 1 && n <= 20 {
			return string(rune(0x2460 + n - 1))
		}
		return strconv.Itoa(n)
	case strings.HasPrefix(scheme, "arabic"):
		rest = scheme[6:]
	}
	if body == "" {
		body = strconv.Itoa(n)
	}
	switch rest {
	case "ParenR":
		return body + ")"
	case "ParenBoth":
		return "(" + body + ")"
	case "Plain":
		return body
	}
	return body + "."
}

// run reads one run of text (a:r) or a field (a:fld). It returns the
// size the run's text is shown at, in points, or 0 if it has no text.
func (t *Text) run(s *xmlscan.Scanner, paraStart, level int, slideNumber bool) float64 {
	start := len(t.Buf)
	var rp RunProps
	t.to.id, t.to.program = t.to.id[:0], false
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Draw, "rPr"):
			readRunProps(s, &rp, &t.to)
		case s.Is(ns.Draw, "t"):
			t.raw = s.AppendText(t.raw[:0])
			if slideNumber && t.SlideNumber != "" {
				t.Buf = append(t.Buf, t.SlideNumber...)
				continue
			}
			if font, sym := t.symbolFonts(&rp, level); font != symbol.None || sym != symbol.None {
				// Text in a symbol font: each character is a code of
				// the font ("J" in Wingdings is a smiling face).
				var missing int
				t.sym, missing = symbol.Translate(t.sym[:0], t.raw, font, sym)
				t.raw, t.sym = t.sym, t.raw
				t.Unmapped += missing
			}
			var removed int
			t.Buf, removed = clean.Append(t.Buf, t.raw, clean.Ligatures)
			t.Removed += removed
		default:
			s.Skip()
		}
	}
	if t.to.program {
		t.Programs++
	}
	if len(t.Buf) == start {
		return 0
	}
	rp.inherit(t.level(level))

	size := rp.Size
	if size == 0 {
		size = defaultSize
	}
	shown := float64(size) / 100 * t.Scale
	// Text the file declares drawn with no ink: no fill and no outline, or
	// a fill with no opacity. Its colour is not compared with anything.
	var reasons gofastcore.Display
	switch {
	case rp.Outline == PaintOther:
		// The letters have an outline, drawn whatever their fill.
	case rp.Ink.Paint == PaintNone:
		reasons = gofastcore.DisplayNoInk
	case rp.Ink.Paint == PaintSolid:
		if _, alpha, ok := t.Palette.Resolve(rp.Ink.Color, nil); ok && alpha <= 0 {
			reasons = gofastcore.DisplayNoInk
		}
	}

	var style gofastcore.Style
	href := ""
	if t.Styles {
		style = rp.On
		if len(t.to.id) > 0 && t.Link != nil {
			href = t.Link(t.to.id)
		}
	}
	if reasons != 0 || style != 0 || href != "" {
		from, to := start-paraStart, len(t.Buf)-paraStart
		// The spans held are those of the paragraph being read.
		if n := len(t.spans); n > 0 {
			if last := &t.spans[n-1]; last.End == from && last.Style == style && last.Href == href && last.Display == reasons {
				last.End = to
				return shown
			}
		}
		// One address may be linked to any number of times: it counts
		// against the text the document may hold each time it is written.
		if href != "" {
			if !t.Budget.Take(len(href)) {
				s.Abort(warn.ErrText)
				return shown
			}
			t.Taken += int64(len(href))
		}
		t.spans = append(t.spans, gofastcore.Span{Start: from, End: to, Style: style, Href: href, Display: reasons})
	}
	return shown
}
