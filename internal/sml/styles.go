package sml

import (
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/numfmt"
	"github.com/AOShei/go-fast-ooxml/internal/symbol"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A cell names a style by number. The style says how the cell's value is
// displayed (its number format) and what the cell looks like, of which
// two things matter here: a font too small to read, and text in the colour
// of the cell's fill.

// cellStyle is what a style means for the text of a cell.
type cellStyle struct {
	format  *numfmt.Format
	general bool              // the format is General
	hidden  gofastcore.Hidden // why text in such a cell cannot be seen
	symbol  symbol.Font       // the cell's font, if it is a symbol font: its text is codes

	// The colour of its text, if its font says one (inkAuto: says it is
	// automatic), and its fill: 0 none, 1 fillColor, 2 not one colour or
	// not known. A table's style gives what these do not.
	inkSet, inkAuto bool
	ink             theme.Color
	fill            uint8
	fillColor       theme.Color
	// With DeepHidden, the colours of a gradient fill (fill 2), which text
	// is judged against as against one colour.
	stops []theme.Color
}

type styleSheet struct {
	styles []cellStyle
	// For tables: the formats (dxfs) the workbook's table styles use,
	// its own table styles, and the theme and palette to resolve the
	// colours of the built-in ones.
	dxfs    []look
	custom  map[string]*customStyle
	resolve func(colorRef) (theme.Color, bool)
}

var plainStyle = cellStyle{format: numfmt.General(), general: true}

// style returns the style with the given number. One the file does not
// define is the plain style.
func (st *styleSheet) style(i int) *cellStyle {
	if st == nil || i < 0 || i >= len(st.styles) {
		return &plainStyle
	}
	return &st.styles[i]
}

// Limits on a styles part. Excel itself stops at 64,000 styles.
const (
	maxStyles  = 1 << 17
	maxFormats = 1 << 13
)

// colorRef is a colour as a style writes it, before the theme and the
// palette are known.
type colorRef struct {
	kind  uint8 // 0 not given, 1 automatic, 2 rgb, 3 theme, 4 indexed
	rgb   theme.Color
	index int
	tint  float64
}

func readColorRef(s *xmlscan.Scanner) colorRef {
	var c colorRef
	if v, ok := s.Attr(xmlscan.NoSpace, "rgb"); ok {
		// Eight digits: transparency first, which is not used.
		if len(v) == 8 {
			v = v[2:]
		}
		if rgb, ok := theme.Hex(v); ok {
			c.kind, c.rgb = 2, rgb
		}
	} else if v, ok := s.Attr(xmlscan.NoSpace, "theme"); ok {
		if n, err := strconv.Atoi(string(v)); err == nil {
			c.kind, c.index = 3, n
		}
	} else if v, ok := s.Attr(xmlscan.NoSpace, "indexed"); ok {
		if n, err := strconv.Atoi(string(v)); err == nil {
			c.kind, c.index = 4, n
		}
	} else if v, ok := s.Attr(xmlscan.NoSpace, "auto"); ok && isTrue(v) {
		c.kind = 1
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "tint"); ok {
		if t, err := strconv.ParseFloat(string(v), 64); err == nil {
			c.tint = t
		}
	}
	return c
}

type fontInfo struct {
	size   float64 // in points; 0 if not given
	color  colorRef
	symbol symbol.Font
}

type fillInfo struct {
	kind  uint8 // 0 none, 1 one colour, 2 something else: a pattern, a gradient
	color colorRef
	stops []colorRef // of a gradient
}

// readStyles reads a styles part.
func readStyles(s *xmlscan.Scanner, th *theme.Scheme, deep bool) *styleSheet {
	codes := map[int]string{}
	type dxfRef struct {
		ink, fill colorRef
		fillKind  uint8
	}
	var dxfs []dxfRef
	var custom map[string]*customStyle
	var fonts []fontInfo
	var fills []fillInfo
	type xf struct{ format, font, fill int }
	var xfs []xf
	indexed := defaultPalette
	for s.Next() != xmlscan.EOF {
		if s.Kind() != xmlscan.Start || s.Depth() != 2 {
			continue
		}
		if s.Space() != ns.Sheet {
			s.Skip()
			continue
		}
		depth := s.Depth()
		switch string(s.Local()) {
		case "numFmts":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || !s.Is(ns.Sheet, "numFmt") || len(codes) >= maxFormats {
					continue
				}
				id, ok := intAttr(s, "numFmtId")
				code, has := s.Attr(xmlscan.NoSpace, "formatCode")
				if ok && has {
					// Text a format writes into a cell is text like any other.
					codes[id], _ = clean.String(code, clean.Ligatures)
				}
			}
		case "fonts":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
					continue
				}
				var f fontInfo
				inner := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= inner {
					if s.Kind() != xmlscan.Start || s.Depth() != inner+1 {
						continue
					}
					switch {
					case s.Is(ns.Sheet, "sz"):
						if v, ok := s.Attr(xmlscan.NoSpace, "val"); ok {
							if size, err := strconv.ParseFloat(string(v), 64); err == nil && size > 0 {
								f.size = size
							}
						}
					case s.Is(ns.Sheet, "color"):
						f.color = readColorRef(s)
					case s.Is(ns.Sheet, "name"):
						v, _ := s.Attr(xmlscan.NoSpace, "val")
						f.symbol = symbol.ByName(string(v))
					}
				}
				if len(fonts) < maxStyles {
					fonts = append(fonts, f)
				}
			}
		case "fills":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
					continue
				}
				var f fillInfo
				inner := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= inner {
					if s.Kind() != xmlscan.Start {
						continue
					}
					switch {
					case s.Depth() == inner+1 && s.Is(ns.Sheet, "patternFill"):
						switch v, _ := s.Attr(xmlscan.NoSpace, "patternType"); string(v) {
						case "", "none":
						case "solid":
							f.kind = 1
						default:
							f.kind = 2 // dots, stripes: not one colour
						}
					case s.Depth() == inner+1:
						f.kind = 2 // a gradient
					case s.Depth() == inner+3 && s.Is(ns.Sheet, "color") && len(f.stops) < 16:
						f.stops = append(f.stops, readColorRef(s)) // a stop of a gradient
					case s.Depth() == inner+2 && s.Is(ns.Sheet, "fgColor"):
						// The colour of a solid fill is its foreground.
						f.color = readColorRef(s)
					}
				}
				if len(fills) < maxStyles {
					fills = append(fills, f)
				}
			}
		case "cellXfs":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+1 || len(xfs) >= maxStyles {
					continue
				}
				var x xf
				x.format, _ = intAttr(s, "numFmtId")
				x.font, _ = intAttr(s, "fontId")
				x.fill, _ = intAttr(s, "fillId")
				xfs = append(xfs, x)
			}
		case "dxfs":
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() == xmlscan.Start && s.Depth() == depth+1 && s.Is(ns.Sheet, "dxf") && len(dxfs) < maxStyles {
					var d dxfRef
					d.ink, d.fill, d.fillKind = readDxf(s)
					dxfs = append(dxfs, d)
				}
			}
		case "tableStyles":
			custom = readTableStyles(s)
		case "colors":
			// A workbook can replace the 56 colours of the old palette.
			var custom []theme.Color
			for s.Next() != xmlscan.EOF && s.Depth() >= depth {
				if s.Kind() != xmlscan.Start || s.Depth() != depth+2 || !s.Is(ns.Sheet, "rgbColor") || len(custom) >= 64 {
					continue
				}
				c := readColorRef(s)
				custom = append(custom, c.rgb)
			}
			if len(custom) > 0 {
				indexed = custom
			}
		default:
			s.Skip()
		}
	}

	resolve := func(c colorRef) (theme.Color, bool) {
		var rgb theme.Color
		switch c.kind {
		case 2:
			rgb = c.rgb
		case 3:
			var ok bool
			if rgb, ok = th.Indexed(c.index); !ok {
				return rgb, false
			}
		case 4:
			// 64 and 65 are the system's text and window colours.
			if c.index < 0 || c.index >= len(indexed) {
				return rgb, false
			}
			rgb = indexed[c.index]
		default:
			return rgb, false
		}
		return theme.Tint(rgb, c.tint), true
	}
	formats := map[int]*numfmt.Format{}
	st := &styleSheet{styles: make([]cellStyle, len(xfs)), custom: custom, resolve: resolve}
	for _, d := range dxfs {
		var l look
		if c, ok := resolve(d.ink); ok {
			l.inkSet, l.ink = true, c
		}
		if l.fill = d.fillKind; l.fill == 1 {
			if c, ok := resolve(d.fill); ok {
				l.fillColor = c
			} else {
				l.fill = 2
			}
		}
		st.dxfs = append(st.dxfs, l)
	}
	for i, x := range xfs {
		f, ok := formats[x.format]
		if !ok {
			code, custom := codes[x.format]
			if !custom {
				code = numfmt.Builtin(x.format)
			}
			f = numfmt.Parse(code)
			formats[x.format] = f
		}
		cs := cellStyle{format: f, general: f == numfmt.General()}
		if x.font >= 0 && x.font < len(fonts) {
			font := fonts[x.font]
			cs.symbol = font.symbol
			if c, ok := resolve(font.color); ok {
				cs.inkSet, cs.ink = true, c
			}
			cs.inkAuto = font.color.kind == 1
		}
		if x.fill >= 0 && x.fill < len(fills) {
			switch fill := fills[x.fill]; fill.kind {
			case 1:
				cs.fill = 2
				if c, ok := resolve(fill.color); ok {
					cs.fill, cs.fillColor = 1, c
				}
			case 2:
				cs.fill = 2
				if deep {
					cs.stops = resolveAll(fill.stops, resolve)
				}
			}
		}
		if x.font >= 0 && x.font < len(fonts) {
			font := fonts[x.font]
			if font.size > 0 && font.size < gofastcore.HiddenTinySize {
				cs.hidden |= gofastcore.HiddenTiny
			}
			// Text in a colour the file names, against a fill of one
			// colour or none. The automatic colour is never the fill's.
			if ink, ok := resolve(font.color); ok {
				behind, known := theme.White, true
				if x.fill >= 0 && x.fill < len(fills) {
					switch fill := fills[x.fill]; fill.kind {
					case 1:
						behind, known = resolve(fill.color)
					case 2:
						known = false
					}
				}
				if known && gofastcore.LowContrast(ink.R, ink.G, ink.B, behind.R, behind.G, behind.B) {
					cs.hidden |= gofastcore.HiddenLowContrast
				}
				if !known && dml.LowOnMost(ink, 1, cs.stops) {
					cs.hidden |= gofastcore.HiddenLowContrast
				}
			}
		}
		st.styles[i] = cs
	}
	return st
}

// resolveAll resolves every colour, or gives nil if any is not known.
func resolveAll(refs []colorRef, resolve func(colorRef) (theme.Color, bool)) []theme.Color {
	if len(refs) == 0 {
		return nil
	}
	out := make([]theme.Color, len(refs))
	for i, c := range refs {
		rgb, ok := resolve(c)
		if !ok {
			return nil
		}
		out[i] = rgb
	}
	return out
}

func intAttr(s *xmlscan.Scanner, name string) (int, bool) {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok {
		return 0, false
	}
	return atoi(v)
}

// atoi reads a non-negative decimal number of at most nine digits.
func atoi(v []byte) (int, bool) {
	if len(v) == 0 || len(v) > 9 {
		return 0, false
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func isTrue(v []byte) bool {
	return len(v) == 1 && v[0] == '1' || string(v) == "true"
}

// defaultPalette is the palette of 56 colours that "indexed" colours
// refer to, with its first eight repeated as Excel has them.
var defaultPalette = palette(
	0x000000, 0xFFFFFF, 0xFF0000, 0x00FF00, 0x0000FF, 0xFFFF00, 0xFF00FF, 0x00FFFF,
	0x000000, 0xFFFFFF, 0xFF0000, 0x00FF00, 0x0000FF, 0xFFFF00, 0xFF00FF, 0x00FFFF,
	0x800000, 0x008000, 0x000080, 0x808000, 0x800080, 0x008080, 0xC0C0C0, 0x808080,
	0x9999FF, 0x993366, 0xFFFFCC, 0xCCFFFF, 0x660066, 0xFF8080, 0x0066CC, 0xCCCCFF,
	0x000080, 0xFF00FF, 0xFFFF00, 0x00FFFF, 0x800080, 0x800000, 0x008080, 0x0000FF,
	0x00CCFF, 0xCCFFFF, 0xCCFFCC, 0xFFFF99, 0x99CCFF, 0xFF99CC, 0xCC99FF, 0xFFCC99,
	0x3366FF, 0x33CCCC, 0x99CC00, 0xFFCC00, 0xFF9900, 0xFF6600, 0x666699, 0x969696,
	0x003366, 0x339966, 0x003300, 0x333300, 0x993300, 0x993366, 0x333399, 0x333333,
)

func palette(rgb ...uint32) []theme.Color {
	out := make([]theme.Color, len(rgb))
	for i, v := range rgb {
		out[i] = theme.Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)}
	}
	return out
}
