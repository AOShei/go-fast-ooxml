package wml

import (
	"errors"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A drawing anchored in a paragraph can float at a place of its own on the
// page: a text box, a picture, a shape. Where that place is follows from
// the page's size and margins, which a section gives at its end, and from
// what the place is measured from. Measured from the page or a margin, it
// is known; from a column, a paragraph or a line, it lies somewhere
// between the margins, and only the range of places it could be is
// known. Without laying out the pages no more is.
//
// So a text box is off the page (HiddenOffPage) if every place it could
// be is off it. It is under a shape (HiddenOccluded) if an opaque picture
// or filled shape anchored in the same paragraph, in front of it, covers
// it wholly: two drawings anchored in one paragraph are on one page, and
// their places are measured alike. Text in the flow of the body has no
// place without layout, and is not checked.

// pageGeom is a section's page: its size and margins, in points.
type pageGeom struct {
	w, h                     float64
	top, bottom, left, right float64
}

// letter is the page of a section that says none: US Letter with the
// margins Word gives it.
var letter = pageGeom{w: 612, h: 792, top: 72, bottom: 72, left: 72, right: 72}

// anchorPos is where a floating drawing is along one direction: an offset
// in points from what the place is measured from.
type anchorPos struct {
	from   string
	offset float64
	set    bool // an offset was given; an alignment keeps it on the page
}

// float is a drawing anchored at a place on the page.
type float struct {
	id         int // its text's blocks carry it, until the place is judged
	section    int // the section it is in
	paragraph  int // the paragraph it is anchored in
	h, v       anchorPos
	w, height  float64
	z          int64 // what is drawn later is in front: behind the text first, then by relative height
	text       bool  // it has text of its own
	cover      bool  // it hides what is under it: an opaque fill
	picture    string
	pictureOK  bool // the picture is known to be opaque, once checked
	pictureSet bool // that was checked
}

// attrFloat marks the blocks of a floating text box until finish judges
// its place; it never reaches the output.
const attrFloat = "\x00float"

// maxFloats bounds the floating drawings kept track of.
const maxFloats = 1 << 14

// readAnchor reads the attributes of wp:anchor into f.
func readAnchor(s *xmlscan.Scanner, f *float) {
	behind := false
	if v, ok := s.Attr(xmlscan.NoSpace, "behindDoc"); ok {
		behind = isOn(v)
	}
	height := int64(0)
	if v, ok := s.Attr(xmlscan.NoSpace, "relativeHeight"); ok {
		if n, err := strconv.ParseInt(string(v), 10, 64); err == nil && n >= 0 {
			height = n
		}
	}
	f.z = height
	if !behind {
		f.z += 1 << 40 // in front of the text, and of all that is behind it
	}
	if v, ok := s.Attr(xmlscan.NoSpace, "simplePos"); ok && isOn(v) {
		// Placed by wp:simplePos, from the page's corner.
		f.h, f.v = anchorPos{from: "simple"}, anchorPos{from: "simple"}
	}
}

// readSimplePos reads wp:simplePos, used where the anchor says so.
func readSimplePos(s *xmlscan.Scanner, f *float) {
	if f.h.from != "simple" {
		return
	}
	if x, ok := emuSigned(s, "x"); ok {
		f.h = anchorPos{from: "page", offset: x, set: true}
	}
	if y, ok := emuSigned(s, "y"); ok {
		f.v = anchorPos{from: "page", offset: y, set: true}
	}
}

// readPosition reads wp:positionH or wp:positionV: what the place is
// measured from, and the offset or alignment.
func readPosition(s *xmlscan.Scanner, pos *anchorPos) {
	if pos.from == "page" && pos.set {
		s.Skip() // placed by simplePos
		return
	}
	from, _ := s.Attr(xmlscan.NoSpace, "relativeFrom")
	*pos = anchorPos{from: string(from)}
	depth := s.Depth()
	for s.Next() != xmlscan.EOF && s.Depth() >= depth {
		if s.Kind() != xmlscan.Start || !s.Is(ns.WordDraw, "posOffset") {
			continue
		}
		v := s.AppendText(nil)
		// The ends of a 32-bit number are what some programs write for a
		// place they do not know: no place.
		if n, err := strconv.ParseInt(string(trimSpace(v)), 10, 64); err == nil && n > -1<<31 && n < 1<<31-1 {
			pos.offset, pos.set = float64(n)/12700, true
		}
	}
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\n' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// emuSigned reads a length in EMU, which may be negative, in points.
func emuSigned(s *xmlscan.Scanner, name string) (float64, bool) {
	v, ok := s.Attr(xmlscan.NoSpace, name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil || n <= -1<<40 || n >= 1<<40 {
		return 0, false
	}
	return float64(n) / 12700, true
}

// origin returns the range of places, in points from the page's edge,
// that a place measured as pos is measured from. ok is false where that
// is not known at all.
func origin(pos anchorPos, g pageGeom, horizontal bool) (lo, hi float64, ok bool) {
	if horizontal {
		switch pos.from {
		case "page", "leftMargin":
			return 0, 0, true
		case "margin":
			return g.left, g.left, true
		case "column", "character":
			return g.left, g.w - g.right, true
		case "rightMargin":
			return g.w - g.right, g.w - g.right, true
		case "insideMargin", "outsideMargin":
			return 0, g.w - g.right, true
		}
		return 0, 0, false
	}
	switch pos.from {
	case "page", "topMargin":
		return 0, 0, true
	case "margin":
		return g.top, g.top, true
	case "bottomMargin":
		return g.h - g.bottom, g.h - g.bottom, true
	case "paragraph", "line":
		return g.top, g.h - g.bottom, true
	case "insideMargin", "outsideMargin":
		return 0, g.h - g.bottom, true
	}
	return 0, 0, false
}

// offPage reports whether every place the drawing could be is off the
// page.
func (f *float) offPage(g pageGeom) bool {
	out := func(pos anchorPos, size, page float64, horizontal bool) bool {
		lo, hi, ok := origin(pos, g, horizontal)
		if !ok || !pos.set || size < 0 {
			return false
		}
		return lo+pos.offset >= page || hi+pos.offset+size <= 0
	}
	return out(f.h, f.w, g.w, true) || out(f.v, f.height, g.h, false)
}

// box returns where two drawings anchored in one paragraph are, in a frame
// they share: along a direction measured from the same thing, the offsets
// alone; otherwise, where both places are known. ok is false where they
// cannot be compared.
func boxes(a, b *float, g pageGeom) (ax0, ay0, bx0, by0 float64, ok bool) {
	axis := func(pa, pb anchorPos, horizontal bool) (float64, float64, bool) {
		if !pa.set || !pb.set {
			return 0, 0, false
		}
		if pa.from == pb.from {
			return pa.offset, pb.offset, true
		}
		alo, ahi, aok := origin(pa, g, horizontal)
		blo, bhi, bok := origin(pb, g, horizontal)
		if !aok || !bok || alo != ahi || blo != bhi {
			return 0, 0, false
		}
		return alo + pa.offset, blo + pb.offset, true
	}
	var okX, okY bool
	ax0, bx0, okX = axis(a.h, b.h, true)
	ay0, by0, okY = axis(a.v, b.v, false)
	return ax0, ay0, bx0, by0, okX && okY
}

// judgeFloats flags the text of floating text boxes that is off the page
// or under an opaque drawing, and takes the marks that tie blocks to
// their drawings off every block.
func (r *reader) judgeFloats(blocks []gofastcore.Block) {
	if len(r.floats) == 0 {
		return
	}
	page := func(section int) pageGeom {
		if len(r.pages) == 0 {
			return letter
		}
		return r.pages[min(section, len(r.pages)-1)]
	}
	hidden := map[int]gofastcore.Hidden{}
	for i := range r.floats {
		f := &r.floats[i]
		if !f.text {
			continue
		}
		g := page(f.section)
		if f.offPage(g) {
			hidden[f.id] |= gofastcore.HiddenOffPage
		}
		// What is in front of it, anchored in the same paragraph.
		for j := range r.floats {
			c := &r.floats[j]
			if j == i || c.paragraph != f.paragraph || c.z <= f.z || !c.cover && c.picture == "" {
				continue
			}
			fx, fy, cx, cy, ok := boxes(f, c, g)
			if !ok || f.w < 0 || f.height < 0 || cx > fx || cy > fy || cx+c.w < fx+f.w || cy+c.height < fy+f.height {
				continue
			}
			if !c.cover && !r.opaquePicture(c) {
				continue
			}
			hidden[f.id] |= gofastcore.HiddenOccluded
			break
		}
	}
	for i := range blocks {
		b := &blocks[i]
		v, ok := b.Attrs[attrFloat]
		if !ok {
			continue
		}
		delete(b.Attrs, attrFloat)
		if len(b.Attrs) == 0 {
			b.Attrs = nil
		}
		id, _ := strconv.Atoi(v)
		if h := hidden[id]; h != 0 && b.Kind != gofastcore.KindImage {
			hideWhole(b, h)
		}
	}
}

// opaquePicture reports whether a drawing's picture is known to have no
// transparency.
func (r *reader) opaquePicture(f *float) bool {
	if !f.pictureSet {
		f.pictureSet = true
		ok, err := media.Opaque(r.pkg, f.picture)
		if errors.Is(err, opc.ErrBudget) {
			r.budget = true
		}
		f.pictureOK = ok
	}
	return f.pictureOK
}

// float records a drawing anchored at a place of its own: a text box,
// whose blocks from boxes on are marked to be judged when the pages are
// known, or a picture or filled shape that may hide one.
func (p *parser) float(f float, boxes int, hasBox bool, fill color, picture string, external bool, w, h float64) {
	f.w, f.height = w, h
	f.cover = fill.set && !fill.unknown
	if picture != "" && !external {
		f.picture = picture
	}
	if hasBox {
		f.id = len(p.r.floats) + 1
		mark := strconv.Itoa(f.id)
		for i := boxes; i < len(p.after); i++ {
			if b := &p.after[i]; b.Kind != gofastcore.KindImage && b.Text != "" || b.Table != nil {
				if b.Attrs == nil {
					b.Attrs = map[string]string{}
				}
				b.Attrs[attrFloat] = mark
				f.text = true
			}
		}
	}
	if f.text || f.cover || f.picture != "" {
		p.r.floats = append(p.r.floats, f)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
