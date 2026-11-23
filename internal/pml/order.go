package pml

import (
	"sort"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
)

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
// outside the slide comes after what is on it. Items
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
