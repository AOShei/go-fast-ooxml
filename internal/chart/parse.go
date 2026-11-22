package chart

import (
	"strings"

	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// axis is what is known of one axis of a chart.
type axis struct {
	title    string
	across   bool // it runs along the bottom or the top: the horizontal axis
	known    bool // its position is stated
	deleted  bool
	category bool // an axis of categories (the newer kind's catScaling)
}

// classic reads a c:chartSpace, whose Start token is current.
func (r *reader) classic(c *Chart) []series {
	s := &r.s
	var list []series
	var axes []axis
	hasTitle, autoDeleted := false, false
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Chart, "date1904"):
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			r.date1904 = isTrue(v, true)
		case s.Is(ns.Chart, "title") && s.Depth() == depth+2:
			// The chart's own title (c:chart/c:title); an axis has one
			// too, read with the axis.
			hasTitle = true
			c.Title = r.title(ns.Chart)
		case s.Is(ns.Chart, "autoTitleDeleted"):
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			autoDeleted = isTrue(v, true)
		case s.Space() == ns.Chart && strings.HasSuffix(string(s.Local()), "Chart") && len(s.Local()) > len("Chart"):
			// A plot: c:barChart, c:lineChart, c:pie3DChart. Its series
			// follow.
			c.kind(strings.TrimSuffix(string(s.Local()), "Chart"))
		case s.Is(ns.Chart, "ser"):
			if len(list) < maxSeries {
				list = append(list, r.series(false))
			} else {
				r.lost.Add(warn.CapChartSeries, 1)
				s.Skip()
			}
		case s.Space() == ns.Chart2012 && strings.HasPrefix(string(s.Local()), "filtered") && strings.HasSuffix(string(s.Local()), "Series"):
			// Series the chart's filter leaves out, kept in an
			// extension: c15:filteredBarSeries/c15:ser.
			fd := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= fd {
				if s.Kind() == xmlscan.Start && s.Is(ns.Chart2012, "ser") {
					if len(list) < maxSeries {
						list = append(list, r.series(true))
					} else {
						r.lost.Add(warn.CapChartSeries, 1)
						s.Skip()
					}
				}
			}
		case s.Is(ns.Chart, "catAx"), s.Is(ns.Chart, "valAx"), s.Is(ns.Chart, "dateAx"), s.Is(ns.Chart, "serAx"):
			if len(axes) < maxAxes {
				axes = append(axes, r.axis())
			} else {
				r.lost.Add(warn.CapChartAxes, 1)
				s.Skip()
			}
		case s.Is(ns.Chart, "txPr"), s.Is(ns.Chart, "spPr"), s.Is(ns.Chart, "printSettings"), s.Is(ns.Chart, "userShapes"), s.Is(ns.Chart, "externalData"):
			s.Skip()
		}
	}
	if !hasTitle || c.Title == "" && autoDeleted {
		// No title is shown.
	} else if c.Title == "" {
		// A title with no text of its own shows the name of the series
		// when there is one.
		shown := 0
		name := ""
		for i := range list {
			if !list[i].hidden {
				shown++
				name = list[i].name
			}
		}
		if shown == 1 {
			c.Title = name
		}
	}
	c.axes(axes)
	return list
}

// axes sets the titles of the horizontal and vertical axes: the first of
// each that is shown and has one.
func (c *Chart) axes(axes []axis) {
	for _, a := range axes {
		if a.deleted || a.title == "" || !a.known {
			continue
		}
		if a.across && c.AxisX == "" {
			c.AxisX = a.title
		} else if !a.across && c.AxisY == "" {
			c.AxisY = a.title
		}
	}
}

func isTrue(v []byte, absent bool) bool {
	switch string(v) {
	case "":
		return absent
	case "1", "true", "on":
		return true
	}
	return false
}

// title reads a title (c:title or cx:title), whose Start token is current:
// its text written out, or the text of the cell it is taken from as the
// cache holds it.
func (r *reader) title(space xmlscan.Space) string {
	s := &r.s
	var b strings.Builder
	paras := 0
	cut := false
	defer func() {
		if cut {
			r.lost.Add(warn.CapChartTitle, 1)
		}
	}()
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Draw, "p"):
			// A paragraph of rich text: its runs and fields.
			if paras > 0 {
				b.WriteByte('\n')
			}
			paras++
			pd := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= pd {
				if s.Kind() == xmlscan.Start {
					switch {
					case s.Is(ns.Draw, "t"):
						r.buf = s.AppendText(r.buf[:0])
						cut = r.titlePiece(&b, r.buf) || cut
					case s.Is(ns.Draw, "br"):
						b.WriteByte('\n')
					}
				}
			}
		case s.Is(space, "v"):
			// c:strCache/c:pt/c:v, or cx:txData/cx:v. A name taken from
			// several cells is shown with a space between them.
			if paras > 0 {
				b.WriteByte(' ')
			}
			paras++
			r.buf = s.AppendText(r.buf[:0])
			cut = r.titlePiece(&b, r.buf) || cut
		case s.Is(space, "txPr"), s.Is(space, "spPr"), s.Is(space, "f"):
			s.Skip()
		}
	}
	t, n := clean.String([]byte(b.String()), clean.Prose)
	r.removed += n
	return t
}

// titlePiece adds a piece of a title to b, unless b already holds
// maxTitle bytes, and reports whether it was left out.
func (r *reader) titlePiece(b *strings.Builder, piece []byte) bool {
	if len(piece) == 0 {
		return false
	}
	if b.Len() >= maxTitle {
		return true
	}
	b.Write(piece)
	return false
}

// axis reads an axis (c:catAx, c:valAx, c:dateAx, c:serAx), whose Start
// token is current.
func (r *reader) axis() axis {
	s := &r.s
	var a axis
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Chart, "title"):
			a.title = r.title(ns.Chart)
		case s.Is(ns.Chart, "delete"):
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			a.deleted = isTrue(v, true)
		case s.Is(ns.Chart, "axPos"):
			v, _ := s.Attr(xmlscan.NoSpace, "val")
			a.known = true
			a.across = string(v) == "b" || string(v) == "t"
		default:
			s.Skip()
		}
	}
	return a
}

// series reads a series (c:ser, or c15:ser in an extension), whose Start
// token is current.
func (r *reader) series(hidden bool) series {
	s := &r.s
	out := series{hidden: hidden}
	hasValues := false
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || s.Depth() != depth+1 {
			continue
		}
		switch {
		case s.Is(ns.Chart, "tx"):
			out.name = r.title(ns.Chart)
		case s.Is(ns.Chart, "cat"), s.Is(ns.Chart, "xVal"):
			out.cats = r.dim()
		case s.Is(ns.Chart, "val"), s.Is(ns.Chart, "yVal"):
			// The values come before the sizes of a bubble chart's
			// bubbles, whatever the order in the file.
			out.vals = append([]dim{r.dim()}, out.vals...)
			hasValues = true
		case s.Is(ns.Chart, "bubbleSize"):
			out.vals = append(out.vals, r.dim())
		default:
			s.Skip()
		}
	}
	if !hasValues {
		// A series with no values still has a column for them, which
		// carries its name, before any sizes of bubbles.
		out.vals = append([]dim{{}}, out.vals...)
	}
	return out
}

// dim reads the data of a series' categories or values (c:cat, c:val,
// c:xVal, c:yVal), whose Start token is current: a reference with its
// cache, or a literal.
func (r *reader) dim() dim {
	s := &r.s
	var d dim
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.Chart, "strCache"), s.Is(ns.Chart, "strLit"):
			d.levels = append(d.levels[:0], r.points(ns.Chart, false, ""))
		case s.Is(ns.Chart, "numCache"), s.Is(ns.Chart, "numLit"):
			d.levels = append(d.levels[:0], r.points(ns.Chart, true, ""))
		case s.Is(ns.Chart, "multiLvlStrCache"):
			d.levels = d.levels[:0]
			md := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= md {
				if s.Kind() == xmlscan.Start && s.Is(ns.Chart, "lvl") {
					if len(d.levels) < maxLevels {
						d.levels = append(d.levels, r.points(ns.Chart, false, ""))
					} else {
						r.lost.Add(warn.CapChartLevels, 1)
						s.Skip()
					}
				}
			}
		case s.Is(ns.Chart, "f"), s.Is(ns.Chart, "extLst"):
			s.Skip()
		}
	}
	return d
}

// newer reads a cx:chartSpace, whose Start token is current. Its data is
// kept apart from its series, which name it by id.
func (r *reader) newer(c *Chart) []series {
	s := &r.s
	type data struct {
		cats dim
		vals []dim
	}
	byID := map[string]*data{}
	var list []series
	var ids []string
	var axes []axis
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start {
			continue
		}
		switch {
		case s.Is(ns.ChartEx, "data"):
			v, _ := s.Attr(xmlscan.NoSpace, "id")
			id := string(v) // copied before the scanner's buffer is reused
			d := &data{}
			dd := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= dd {
				if s.Kind() != xmlscan.Start {
					continue
				}
				numeric := s.Is(ns.ChartEx, "numDim")
				if !numeric && !s.Is(ns.ChartEx, "strDim") {
					continue
				}
				typ, _ := s.Attr(xmlscan.NoSpace, "type")
				var levels [][]string
				ld := s.Depth()
				for s.Next() != xmlscan.EOF && s.Depth() >= ld {
					if s.Kind() == xmlscan.Start && s.Is(ns.ChartEx, "lvl") {
						if len(levels) < maxLevels {
							levels = append(levels, r.points(ns.ChartEx, numeric, ""))
						} else {
							r.lost.Add(warn.CapChartLevels, 1)
							s.Skip()
						}
					}
				}
				if string(typ) == "cat" && !numeric {
					d.cats = dim{levels: levels}
				} else if len(d.vals) < maxValues {
					d.vals = append(d.vals, dim{levels: levels})
				} else {
					r.lost.Add(warn.CapChartValues, 1)
				}
			}
			if len(byID) < maxSeries {
				byID[id] = d
			} else {
				r.lost.Add(warn.CapChartSeries, 1)
			}
		case s.Is(ns.ChartEx, "title") && s.Depth() == depth+2:
			c.Title = r.title(ns.ChartEx)
		case s.Is(ns.ChartEx, "series"):
			if len(list) >= maxSeries {
				r.lost.Add(warn.CapChartSeries, 1)
				s.Skip()
				continue
			}
			if v, ok := s.Attr(xmlscan.NoSpace, "layoutId"); ok {
				c.kind(string(v))
			}
			ser := series{}
			if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok {
				ser.hidden = isTrue(v, false)
			}
			id := ""
			sd := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= sd {
				if s.Kind() != xmlscan.Start || s.Depth() != sd+1 {
					continue
				}
				switch {
				case s.Is(ns.ChartEx, "tx"):
					ser.name = r.title(ns.ChartEx)
				case s.Is(ns.ChartEx, "dataId"):
					v, _ := s.Attr(xmlscan.NoSpace, "val")
					id = "\x00" + string(v) // set, even if empty
					s.Skip()
				default:
					s.Skip()
				}
			}
			if id == "" {
				// A series drawn from another's data, such as the line
				// of a Pareto chart: nothing of its own.
				continue
			}
			list = append(list, ser)
			ids = append(ids, id[1:])
		case s.Is(ns.ChartEx, "axis"):
			a := axis{known: true}
			if v, ok := s.Attr(xmlscan.NoSpace, "hidden"); ok {
				a.deleted = isTrue(v, false)
			}
			ad := s.Depth()
			for s.Next() != xmlscan.EOF && s.Depth() >= ad {
				if s.Kind() != xmlscan.Start || s.Depth() != ad+1 {
					continue
				}
				switch {
				case s.Is(ns.ChartEx, "catScaling"):
					a.category, a.across = true, true
					s.Skip()
				case s.Is(ns.ChartEx, "title"):
					a.title = r.title(ns.ChartEx)
				default:
					s.Skip()
				}
			}
			if len(axes) < maxAxes {
				axes = append(axes, a)
			} else {
				r.lost.Add(warn.CapChartAxes, 1)
			}
		case s.Is(ns.ChartEx, "txPr"), s.Is(ns.ChartEx, "spPr"), s.Is(ns.ChartEx, "externalData"):
			s.Skip()
		}
	}
	for i := range list {
		if d := byID[ids[i]]; d != nil {
			list[i].cats, list[i].vals = d.cats, d.vals
		}
	}
	c.axes(axes)
	return list
}
