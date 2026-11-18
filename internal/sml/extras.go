package sml

import (
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/hidden"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// What hangs on a sheet besides its cells: the tables it declares, the
// comments on its cells, and the shapes and pictures that float over it.

// declaredTable is a table the workbook declares: where it is and how
// many of its rows are headers.
type declaredTable struct {
	at         rect
	headerRows int
}

// declared reads the sheet's table parts.
func (sh *sheet) declared() []declaredTable {
	var out []declaredTable
	for _, target := range sh.tables {
		sh.load(target, func(s *xmlscan.Scanner) {
			for s.Next() != xmlscan.EOF {
				if s.Kind() != xmlscan.Start {
					continue
				}
				// Everything needed is on the root element.
				if !s.Is(ns.Sheet, "table") {
					return
				}
				ref, ok := s.Attr(xmlscan.NoSpace, "ref")
				if !ok {
					return
				}
				at, ok := parseRange(ref)
				if !ok {
					return
				}
				t := declaredTable{at: at, headerRows: 1}
				if n, ok := intAttr(s, "headerRowCount"); ok {
					t.headerRows = min(n, 1)
				}
				out = append(out, t)
				return
			}
		})
	}
	return out
}

// readPersons reads the people who wrote threaded comments, by id.
func readPersons(s *xmlscan.Scanner) map[string]string {
	persons := map[string]string{}
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Threaded, "person") || len(persons) >= maxAuthors {
			continue
		}
		id, ok1 := s.Attr(xmlscan.NoSpace, "id")
		name, ok2 := s.Attr(xmlscan.NoSpace, "displayName")
		if ok1 && ok2 && len(id) <= 64 {
			persons[string(id)] = clean.Label(string(name))
		}
	}
	return persons
}

const (
	maxAuthors  = 1 << 12
	maxComments = 1 << 18
)

// comments reads the comments on the sheet's cells as note blocks. A
// workbook has two kinds: notes, and threaded comments with replies. A
// threaded comment is also stored as a note that tells older programs
// they cannot show it; that stand-in is left out.
func (sh *sheet) comments() []placed {
	var out []placed
	threaded := map[int64]bool{}
	add := func(row, col int32, raw []byte, author, date string) {
		if len(out) >= maxComments {
			return
		}
		text, removed := cellText(sh.out[:0], raw)
		sh.out = text
		sh.removed += removed
		if len(text) == 0 {
			return
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: string(text), Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}}
		if author != "" {
			b.Attrs[gofastcore.AttrAuthor] = author
		}
		if date != "" {
			b.Attrs[gofastcore.AttrDate] = date
		}
		b.Loc.Range = cellName(row, col)
		out = append(out, placed{row: row, col: col, block: b})
	}
	for _, rel := range sh.list {
		if rel.External || !ns.IsRel(rel.Type, ns.RelThreadedComment) {
			continue
		}
		sh.load(rel.Target, func(s *xmlscan.Scanner) {
			for s.Next() != xmlscan.EOF {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Threaded, "threadedComment") {
					continue
				}
				ref, _ := s.Attr(xmlscan.NoSpace, "ref")
				col, row, ok := parseRef(ref)
				author, date := "", ""
				if v, has := s.Attr(xmlscan.NoSpace, "personId"); has {
					author = sh.wb.persons[string(v)]
				}
				if v, has := s.Attr(xmlscan.NoSpace, "dT"); has {
					date = clean.Label(string(v))
				}
				raw := sh.raw[:0]
				depth := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= depth {
					if s.Kind() == xmlscan.Start && s.Is(ns.Threaded, "text") {
						raw = s.AppendText(raw)
					}
				}
				sh.raw = raw
				if ok {
					threaded[int64(row)<<32|int64(col)] = true
					add(row, col, raw, author, date)
				}
			}
		})
	}
	for _, rel := range sh.list {
		if rel.External || !ns.IsRel(rel.Type, ns.RelComments) {
			continue
		}
		sh.load(rel.Target, func(s *xmlscan.Scanner) {
			var authors []string
			for s.Next() != xmlscan.EOF {
				if s.Kind() != xmlscan.Start || s.Depth() != 3 {
					continue
				}
				switch {
				case s.Is(ns.Sheet, "author"):
					sh.raw = s.AppendText(sh.raw[:0])
					if len(authors) < maxAuthors {
						authors = append(authors, clean.Label(string(sh.raw)))
					}
				case s.Is(ns.Sheet, "comment"):
					ref, _ := s.Attr(xmlscan.NoSpace, "ref")
					col, row, ok := parseRef(ref)
					author := ""
					if i, has := intAttr(s, "authorId"); has && i < len(authors) {
						author = authors[i]
					}
					raw := sh.raw[:0]
					depth := s.Depth()
					for s.Next() != xmlscan.EOF && s.Depth() >= depth {
						if s.Kind() == xmlscan.Start && s.Is(ns.Sheet, "text") {
							raw = readRichText(s, raw)
						}
					}
					sh.raw = raw
					if ok && !threaded[int64(row)<<32|int64(col)] {
						add(row, col, raw, author, "")
					}
				default:
					s.Skip()
				}
			}
		})
	}
	return out
}

// fill is what text in a shape is seen against.
type fill struct {
	color theme.Color
	known bool // false: a gradient, a picture, an adjusted colour
}

var onWhite = fill{color: theme.White, known: true}

// drawingNames maps the names of theme colours in a drawing to the names
// of the colours in the theme part.
var drawingNames = map[string]string{
	"dk1": "dk1", "lt1": "lt1", "dk2": "dk2", "lt2": "lt2",
	"tx1": "dk1", "bg1": "lt1", "tx2": "dk2", "bg2": "lt2",
	"accent1": "accent1", "accent2": "accent2", "accent3": "accent3",
	"accent4": "accent4", "accent5": "accent5", "accent6": "accent6",
	"hlink": "hlink", "folHlink": "folHlink",
}

// drawingColor reads the colour inside a fill element such as a:solidFill
// and consumes the element. A colour that is adjusted (lightened, made
// partly transparent) is not known: the adjustments are not computed.
func drawingColor(s *xmlscan.Scanner, th *theme.Scheme) fill {
	var f fill
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		if s.Depth() > depth+1 {
			f.known = false // an adjustment
			continue
		}
		if s.Space() != ns.Draw {
			continue
		}
		switch string(s.Local()) {
		case "srgbClr":
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			f.color, f.known = theme.Hex(v)
		case "sysClr":
			v, _ := s.Attr(xmlscan.NoSpace, "lastClr")
			f.color, f.known = theme.Hex(v)
		case "schemeClr":
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			f.color, f.known = th.Named(drawingNames[string(v)])
		}
	}
	return f
}

// drawing reads a drawing part: the text of its shapes as paragraphs, and
// its pictures. Each block sits at the cell its shape is anchored to. It
// reports whether there are pictures that were not asked for.
func (sh *sheet) drawing(target string, out []placed) ([]placed, bool) {
	rels, err := sh.wb.pkg.Rels(target)
	sh.noteErr(opc.RelsName(target), err)
	byID := make(map[string]opc.Rel, len(rels))
	for _, rel := range rels {
		byID[rel.ID] = rel
	}
	sawImage := false
	th := sh.wb.theme
	sh.load(target, func(s *xmlscan.Scanner) {
		var row, col int32 // the cell the current anchor starts at
		var alt string
		var unseen bool   // the current shape is marked hidden
		behind := onWhite // what the current shape's text is seen against
		ownFill := false  // set by the shape itself, not by its style
		props := 0        // depth of the shape properties being read
		choice := -1      // blocks there were when the current choice of alternatives began
		for s.Next() != xmlscan.EOF {
			if s.Kind() != xmlscan.Start {
				continue
			}
			if props > 0 && s.Depth() <= props {
				props = 0
			}
			switch {
			case s.Is(ns.MC, "Choice"):
				choice = len(out)
			case s.Is(ns.MC, "Fallback"):
				// The other form of what the choice before it holds: read
				// only if nothing was read there.
				if choice >= 0 && len(out) > choice {
					s.Skip()
				}
				choice = -1
			case s.Is(ns.SheetDraw, "from"):
				depth := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= depth {
					if s.Kind() != xmlscan.Start {
						continue
					}
					switch {
					case s.Is(ns.SheetDraw, "col"):
						sh.raw = s.AppendText(sh.raw[:0])
						if n, ok := atoi(sh.raw); ok {
							col = int32(min(n, maxCols-1))
						}
					case s.Is(ns.SheetDraw, "row"):
						sh.raw = s.AppendText(sh.raw[:0])
						if n, ok := atoi(sh.raw); ok {
							row = int32(min(n, maxRows-1))
						}
					}
				}
			case s.Is(ns.SheetDraw, "sp"), s.Is(ns.SheetDraw, "pic"), s.Is(ns.SheetDraw, "graphicFrame"):
				alt, unseen, behind, ownFill, props = "", false, onWhite, false, 0
			case s.Is(ns.SheetDraw, "cNvPr"):
				if v, ok := s.Attr(xmlscan.NoSpace, "descr"); ok && len(v) > 0 {
					alt, _ = clean.String(v, clean.Prose)
				}
				if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok {
					unseen = isTrue(v)
				}
			case s.Is(ns.SheetDraw, "spPr"):
				props = s.Depth()
			case props > 0 && s.Depth() == props+1 && s.Space() == ns.Draw:
				switch string(s.Local()) {
				case "solidFill":
					behind, ownFill = drawingColor(s, th), true
				case "noFill":
					behind, ownFill = onWhite, true // the sheet shows through
				case "gradFill", "pattFill", "blipFill", "grpFill":
					behind, ownFill = fill{}, true
				case "ln":
					s.Skip() // the outline has a fill of its own
				}
			case s.Is(ns.Draw, "fillRef"):
				// The fill the shape's style gives it, unless it has its own.
				if idx, ok := s.Attr(xmlscan.NoSpace, "idx"); ok && string(idx) != "0" && !ownFill {
					behind = drawingColor(s, th)
				}
			case s.Is(ns.SheetDraw, "txBody"):
				out = sh.textBody(s, out, row, col, behind, unseen)
			case s.Is(ns.Draw, "blip"):
				id, ok := s.Attr(ns.Rel, "embed")
				if !ok {
					id, ok = s.Attr(ns.Rel, "link")
				}
				rel, found := byID[string(id)]
				if !ok || !found {
					continue
				}
				if sh.wb.opts.Images == gofastcore.ImagesNone {
					sawImage = true
					continue
				}
				var b gofastcore.Block
				if rel.External {
					// A picture that is not in the file: named, not fetched.
					b = gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: &gofastcore.Image{}, Attrs: map[string]string{"href": clean.Label(rel.Target)}}
				} else {
					b = sh.wb.image(rel.Target, alt)
				}
				if unseen {
					b.Hidden = gofastcore.HiddenInvisible
				}
				b.Loc.Range = cellName(row, col)
				out = append(out, placed{row: row, col: col, block: b})
			case s.Is(ns.Chart, "chart"):
				sh.unread["chart"]++
			case s.Is(ns.Diagram, "relIds"):
				sh.unread["diagram"]++
			}
		}
	})
	return out, sawImage
}

// textBody reads the text of a shape (the element whose Start token is
// current): one paragraph block for each of its paragraphs that has text.
// Text too small to read, or in the colour of what is behind it, is
// marked; so is all of it if the shape is hidden.
func (sh *sheet) textBody(s *xmlscan.Scanner, out []placed, row, col int32, behind fill, unseen bool) []placed {
	th := sh.wb.theme
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || !s.Is(ns.Draw, "p") {
			continue
		}
		text := sh.out[:0]
		var spans []gofastcore.Span
		para := s.Depth()
		for s.Next() != xmlscan.EOF && s.Depth() >= para {
			if s.Kind() != xmlscan.Start || s.Depth() != para+1 {
				continue
			}
			switch {
			case s.Is(ns.Draw, "br"):
				text = append(text, '\n')
			case s.Is(ns.Draw, "r"), s.Is(ns.Draw, "fld"):
				start := len(text)
				var reasons gofastcore.Hidden
				run := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= run {
					if s.Kind() != xmlscan.Start || s.Depth() != run+1 {
						continue
					}
					switch {
					case s.Is(ns.Draw, "rPr"):
						// Sizes are in hundredths of a point.
						if size, ok := intAttr(s, "sz"); ok && float64(size)/100 < gofastcore.HiddenTinySize {
							reasons |= gofastcore.HiddenTiny
						}
						props := s.Depth()
						for s.Next() != xmlscan.EOF && s.Depth() >= props {
							if s.Kind() != xmlscan.Start || s.Depth() != props+1 {
								continue
							}
							if s.Is(ns.Draw, "solidFill") {
								ink := drawingColor(s, th)
								if ink.known && behind.known && gofastcore.LowContrast(ink.color.R, ink.color.G, ink.color.B, behind.color.R, behind.color.G, behind.color.B) {
									reasons |= gofastcore.HiddenLowContrast
								}
							} else {
								s.Skip()
							}
						}
					case s.Is(ns.Draw, "t"):
						sh.raw = s.AppendText(sh.raw[:0])
						var removed int
						text, removed = clean.Append(text, sh.raw, clean.Ligatures)
						sh.removed += removed
					default:
						s.Skip()
					}
				}
				if reasons != 0 && len(text) > start {
					spans = append(spans, gofastcore.Span{Start: start, End: len(text), Hidden: reasons})
				}
			default:
				s.Skip()
			}
		}
		sh.out = text
		// Trimmed, with the marked ranges moved to match.
		from, to := 0, len(text)
		for to > 0 {
			r, size := utf8.DecodeLastRune(text[:to])
			if !unicode.IsSpace(r) {
				break
			}
			to -= size
		}
		for from < to {
			r, size := utf8.DecodeRune(text[from:to])
			if !unicode.IsSpace(r) {
				break
			}
			from += size
		}
		if from == to {
			continue
		}
		b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: string(text[from:to])}
		b.Spans, b.Hidden = hidden.Settle(spans, from, to, text)
		if unseen {
			// All of it, whatever is true of parts.
			b.Hidden |= gofastcore.HiddenInvisible
		}
		b.Loc.Range = cellName(row, col)
		out = append(out, placed{row: row, col: col, block: b})
	}
	return out
}
