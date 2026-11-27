package vsd

import (
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
)

// The bits of a Character row's Style cell.
const (
	styleBold      = 1
	styleItalic    = 2
	styleUnderline = 4
)

// run is a stretch of a paragraph's text in one format.
type run struct {
	start, end int
	style      gofastcore.Style
	display    gofastcore.Display
}

// para is a paragraph of a shape's text as it is put together.
type para struct {
	buf  []byte
	runs []run
	ix   int32 // its Paragraph row
	set  bool  // ix is settled: the paragraph has text
}

// format returns the style and display reasons of the runs that take
// the format of the Character row ix.
func (v view) format(ix int32) (gofastcore.Style, gofastcore.Display) {
	var st gofastcore.Style
	var disp gofastcore.Display
	if x, ok := v.rowCell(sChar, ix, "Style"); ok {
		if n, err := strconv.Atoi(x); err == nil {
			if n&styleBold != 0 {
				st |= gofastcore.StyleBold
			}
			if n&styleItalic != 0 {
				st |= gofastcore.StyleItalic
			}
			if n&styleUnderline != 0 {
				st |= gofastcore.StyleUnderline
			}
		}
	}
	if x, _ := v.rowCell(sChar, ix, "DoubleUnderline"); x == "1" {
		st |= gofastcore.StyleUnderline
	}
	if x, _ := v.rowCell(sChar, ix, "Strikethru"); x == "1" {
		st |= gofastcore.StyleStrike
	}
	switch x, _ := v.rowCell(sChar, ix, "Pos"); x {
	case "1":
		st |= gofastcore.StyleSuper
	case "2":
		st |= gofastcore.StyleSub
	}
	if x, ok := v.rowCell(sChar, ix, "ColorTrans"); ok {
		if f, err := strconv.ParseFloat(x, 64); err == nil && f >= 1 {
			disp |= gofastcore.DisplayNoInk // fully transparent letters
		}
	}
	return st, disp
}

// link returns the address the shape links to: its default link, else
// its first, with its place in the target after a #.
func (v view) link() string {
	rows := v.namedRows(sHyperlink)
	pick := -1
	for i := range rows {
		addr, _ := rows[i].cell("Address")
		sub, _ := rows[i].cell("SubAddress")
		if addr == "" && sub == "" {
			continue
		}
		if pick < 0 {
			pick = i
		}
		if x, _ := rows[i].cell("Default"); x == "1" {
			pick = i
			break
		}
	}
	if pick < 0 {
		return ""
	}
	addr, _ := rows[pick].cell("Address")
	if sub, _ := rows[pick].cell("SubAddress"); sub != "" {
		addr += "#" + sub
	}
	return addr
}

// shapeText adds the paragraphs of a shape's text.
func (pg *page) shapeText(v view, box dml.Rect, boxed, hidden bool) {
	toks, inherited := v.text()
	if len(toks) == 0 {
		return
	}
	spans := pg.d.opts.Spans
	href := ""
	if spans {
		if a := v.link(); a != "" {
			href = pg.lost.Address(a)
		}
	}
	var paras []para
	cur := para{}
	char := int32(0)
	style, disp := v.format(0)
	paraIX := int32(0)
	add := func(text string) {
		for len(text) > 0 {
			i := 0
			for i < len(text) && text[i] != '\n' && text[i] != '\r' {
				i++
			}
			if i > 0 {
				if !cur.set {
					cur.ix, cur.set = paraIX, true
				}
				start := len(cur.buf)
				var removed int
				cur.buf, removed = clean.Append(cur.buf, []byte(text[:i]), clean.Ligatures)
				pg.removed += removed
				if n := len(cur.runs); n > 0 && cur.runs[n-1].end == start && cur.runs[n-1].style == style && cur.runs[n-1].display == disp {
					cur.runs[n-1].end = len(cur.buf)
				} else {
					cur.runs = append(cur.runs, run{start: start, end: len(cur.buf), style: style, display: disp})
				}
			}
			if i == len(text) {
				return
			}
			// A paragraph ends: at a line feed, or a carriage return not
			// followed by one.
			if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			if !cur.set {
				cur.ix = paraIX
			}
			paras = append(paras, cur)
			cur = para{}
			text = text[i+1:]
		}
	}
	for _, t := range toks {
		switch t.kind {
		case tText:
			add(t.text)
		case tChar:
			char = t.ix
			style, disp = v.format(char)
		case tPara:
			paraIX = t.ix
			if !cur.set {
				cur.ix = paraIX
			}
		case tField:
			if x, ok := v.rowCell(sField, t.ix, "Value"); ok {
				add(x)
			} else {
				add(t.text)
			}
		}
	}
	if len(cur.buf) > 0 {
		paras = append(paras, cur)
	}

	var made []gofastcore.Block
	for i := range paras {
		b, ok := pg.block(v, &paras[i], spans, href)
		if !ok {
			continue
		}
		if boxed {
			b.Loc.BBox = gofastcore.Rect(box)
		}
		if hidden {
			b.Display |= gofastcore.DisplayHidden
		}
		made = append(made, b)
	}
	if len(made) == 0 {
		return
	}
	if inherited {
		// The master's text is on every instance of it: each copy counts.
		if n := int64(len(made)); pg.d.repeated.Add(-n) < 0 {
			pg.d.repeated.Add(n)
			pg.repeat = true
			return
		}
	}
	for _, b := range made {
		if !pg.room.Take(len(b.Text)) {
			pg.full = true
			return
		}
		pg.blocks = append(pg.blocks, b)
	}
}

// block makes a block of a paragraph; ok is false if it has no text.
func (pg *page) block(v view, p *para, spans bool, href string) (gofastcore.Block, bool) {
	buf := p.buf
	lead := 0
	for lead < len(buf) {
		r, size := utf8.DecodeRune(buf[lead:])
		if !unicode.IsSpace(r) {
			break
		}
		lead += size
	}
	end := len(buf)
	for end > lead {
		r, size := utf8.DecodeLastRune(buf[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	if end <= lead {
		return gofastcore.Block{}, false
	}
	b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: string(buf[lead:end])}
	for _, r := range p.runs {
		s, e := max(r.start, lead)-lead, min(r.end, end)-lead
		if s >= e {
			continue
		}
		style := r.style
		if !spans {
			style = 0
		}
		if style == 0 && r.display == 0 {
			continue
		}
		b.Spans = append(b.Spans, gofastcore.Span{Start: s, End: e, Style: style, Display: r.display})
		b.Display |= r.display
	}
	if href != "" {
		b.Spans = append(b.Spans, gofastcore.Span{Start: 0, End: len(b.Text), Href: href})
	}
	if x, ok := v.rowCell(sPara, p.ix, "Bullet"); ok && x != "" && x != "0" {
		b.Kind, b.Level = gofastcore.KindListItem, 1
		b.Marker = "•"
		if str, ok := v.rowCell(sPara, p.ix, "BulletStr"); ok && str != "" {
			if m, _ := clean.String([]byte(str), clean.Prose); m != "" && len(m) <= 64 {
				b.Marker = m
			}
		}
	}
	return b, true
}
