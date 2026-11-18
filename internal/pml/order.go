package pml

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"sort"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
)

// cover is an area that hides what was drawn under it.
type cover struct {
	box     dml.Rect
	picture string // not empty: a picture, which hides only if it has no transparency
}

// occlude marks the text of every shape that lies wholly under a later
// shape that hides it: a rectangle filled with one opaque colour, or a
// picture with no transparency. Shapes are drawn in the order they are
// stored, so later means on top. A shape that an animation acts on is not
// counted as hiding anything: it is not there for the whole of the show.
func (pg *page) occlude() {
	var covers []cover
	for i := len(pg.shapes) - 1; i >= 0; i-- {
		sh := &pg.shapes[i]
		if sh.text {
			for k := 0; k < len(covers); k++ {
				if pg.effort--; pg.effort < 0 {
					pg.skipped++
					break
				}
				c := &covers[k]
				if !c.box.Contains(sh.box) {
					continue
				}
				if c.picture != "" {
					if !pg.p.opaquePicture(c.picture) {
						covers = append(covers[:k], covers[k+1:]...)
						k--
						continue
					}
					c.picture = ""
				}
				for j := sh.first; j < sh.end; j++ {
					if pg.blocks[j].Kind != gofastcore.KindImage {
						pg.blocks[j].Hidden |= gofastcore.HiddenOccluded
					}
				}
				break
			}
		}
		if !sh.boxed || !(sh.cover || sh.picture != "") || pg.moves(sh) {
			continue
		}
		// One that lies inside a cover already known adds nothing: what it
		// hides, that one hides.
		inside := false
		for k := range covers {
			if pg.effort--; pg.effort < 0 {
				break
			}
			if covers[k].picture == "" && covers[k].box.Contains(sh.box) {
				inside = true
				break
			}
		}
		if !inside {
			covers = append(covers, cover{box: sh.box, picture: sh.picture})
		}
	}
}

// moves reports whether an animation acts on the shape or on a group it
// is in.
func (pg *page) moves(sh *shape) bool {
	if pg.allAnimated {
		return true
	}
	for _, id := range sh.ids {
		if _, found := slices.BinarySearch(pg.animated, id); found && id != 0 {
			return true
		}
	}
	return false
}

// opaquePicture reports whether the picture in the named part is known
// to have no transparency: a JPEG, or a PNG with neither an alpha channel
// nor a transparent colour. Of any other picture it is not known, and
// the answer is no.
func (p *presentation) opaquePicture(name string) bool {
	p.mu.Lock()
	opaque, ok := p.opaque[name]
	p.mu.Unlock()
	if ok {
		return opaque
	}
	if rc, err := p.pkg.Open(name); err == nil {
		head := make([]byte, 4096)
		n, err := io.ReadFull(rc, head)
		rc.Close()
		if errors.Is(err, opc.ErrBudget) {
			p.exceeded.Store(true)
		}
		opaque = opaqueHeader(head[:n])
	}
	p.mu.Lock()
	p.opaque[name] = opaque
	p.mu.Unlock()
	return opaque
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// opaqueHeader decides from the start of a picture file whether the
// picture has no transparency.
func opaqueHeader(head []byte) bool {
	if len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF {
		return true // JPEG has no transparency
	}
	if !bytes.HasPrefix(head, pngSignature) {
		return false
	}
	// The chunks before the image data: the header says whether there is
	// an alpha channel, and a tRNS chunk makes a colour transparent.
	for at := len(pngSignature); at+8 <= len(head); {
		size := int(head[at])<<24 | int(head[at+1])<<16 | int(head[at+2])<<8 | int(head[at+3])
		kind := string(head[at+4 : at+8])
		switch kind {
		case "IHDR":
			if at+8+10 > len(head) {
				return false
			}
			if colorType := head[at+8+9]; colorType == 4 || colorType == 6 {
				return false
			}
		case "tRNS":
			return false
		case "IDAT":
			return true
		}
		if size < 0 || size > len(head) {
			return false // the image data starts further in than was read
		}
		at += 12 + size
	}
	return false
}

// item is something to be put in reading order: a shape, or a top-level
// shape or group with everything in it.
type item struct {
	box        dml.Rect
	rank       uint8 // onSlide, offSlide or unplaced
	first, end int   // what it stands for: shapes of a unit, or blocks of a shape
}

const (
	onSlide  uint8 = iota
	offSlide       // wholly outside the slide
	unplaced       // where it is is not known
)

// rank says which of the three an item in the given box is.
func (pg *page) rank(box dml.Rect, boxed bool) uint8 {
	switch w, h := pg.p.width, pg.p.height; {
	case !boxed:
		return unplaced
	case w > 0 && h > 0 && (box.X1 <= 0 || box.Y1 <= 0 || box.X0 >= w || box.Y0 >= h):
		return offSlide
	}
	return onSlide
}

// inOrder sorts items into reading order: from the top down, and from
// left to right among those that start beside each other, which are
// those that start above the middle of the first of them. What lies
// outside the slide is not seen, and comes after what is on it. Items
// whose place is not known come last, in the order they are stored.
func inOrder(items []item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := &items[i], &items[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.rank == unplaced {
			return false
		}
		if a.box.Y0 != b.box.Y0 {
			return a.box.Y0 < b.box.Y0
		}
		return a.box.X0 < b.box.X0
	})
	for i := 0; i < len(items) && items[i].rank != unplaced; {
		middle := (items[i].box.Y0 + items[i].box.Y1) / 2
		j := i + 1
		for j < len(items) && items[j].rank == items[i].rank && items[j].box.Y0 < middle {
			j++
		}
		if j-i > 1 {
			row := items[i:j]
			sort.SliceStable(row, func(a, b int) bool { return row[a].box.X0 < row[b].box.X0 })
		}
		i = j
	}
}

// arrange returns the page's blocks in reading order: the title first,
// then the rest by where they are on the slide. A group is read as one
// thing, at the place of the group, and so is whatever a top-level shape
// holds.
func (pg *page) arrange() []gofastcore.Block {
	pg.occlude()
	if len(pg.blocks) == 0 {
		return nil
	}
	// The units: runs of shapes that belong to one top-level shape.
	var units, members []item
	title := -1
	for i := 0; i < len(pg.shapes); {
		j := i
		u := item{first: len(members)}
		boxed := false
		for ; j < len(pg.shapes) && pg.shapes[j].unit == pg.shapes[i].unit; j++ {
			sh := &pg.shapes[j]
			if sh.first == sh.end {
				continue
			}
			members = append(members, item{box: sh.box, rank: pg.rank(sh.box, sh.boxed), first: sh.first, end: sh.end})
			if sh.boxed {
				if !boxed {
					u.box, boxed = sh.box, true
				} else {
					u.box = dml.Rect{X0: min(u.box.X0, sh.box.X0), Y0: min(u.box.Y0, sh.box.Y0), X1: max(u.box.X1, sh.box.X1), Y1: max(u.box.Y1, sh.box.Y1)}
				}
			}
			if sh.title && title < 0 {
				title = len(units)
			}
		}
		u.end, u.rank = len(members), pg.rank(u.box, boxed)
		if u.end > u.first {
			units = append(units, u)
		}
		i = j
	}
	out := make([]gofastcore.Block, 0, len(pg.blocks))
	emit := func(u item) {
		inside := members[u.first:u.end]
		if len(inside) > 1 {
			inOrder(inside)
		}
		for _, m := range inside {
			out = append(out, pg.blocks[m.first:m.end]...)
		}
	}
	if title >= 0 {
		emit(units[title])
		units = append(units[:title], units[title+1:]...)
	}
	inOrder(units)
	for _, u := range units {
		emit(u)
	}
	return out
}
