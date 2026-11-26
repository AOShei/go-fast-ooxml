package pml

import (
	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/order"
)

type item = order.Item

// rank says where an item in the given box is, as far as order goes.
func (pg *page) rank(box dml.Rect, boxed bool) uint8 {
	return order.Rank(box, boxed, pg.p.width, pg.p.height)
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
		u := item{First: len(members)}
		boxed := false
		for ; j < len(pg.shapes) && pg.shapes[j].unit == pg.shapes[i].unit; j++ {
			sh := &pg.shapes[j]
			if sh.first == sh.end {
				continue
			}
			members = append(members, item{Box: sh.box, Rank: pg.rank(sh.box, sh.boxed), First: sh.first, End: sh.end})
			if sh.boxed {
				if !boxed {
					u.Box, boxed = sh.box, true
				} else {
					u.Box = dml.Rect{X0: min(u.Box.X0, sh.box.X0), Y0: min(u.Box.Y0, sh.box.Y0), X1: max(u.Box.X1, sh.box.X1), Y1: max(u.Box.Y1, sh.box.Y1)}
				}
			}
			if sh.title && title < 0 {
				title = len(units)
			}
		}
		u.End, u.Rank = len(members), pg.rank(u.Box, boxed)
		if u.End > u.First {
			units = append(units, u)
		}
		i = j
	}
	out := make([]gofastcore.Block, 0, len(pg.blocks))
	emit := func(u item) {
		inside := members[u.First:u.End]
		if len(inside) > 1 {
			order.Sort(inside)
		}
		for _, m := range inside {
			out = append(out, pg.blocks[m.First:m.End]...)
		}
	}
	if title >= 0 {
		emit(units[title])
		units = append(units[:title], units[title+1:]...)
	}
	order.Sort(units)
	for _, u := range units {
		emit(u)
	}
	return out
}
