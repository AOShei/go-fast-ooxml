// Package order puts the shapes of a drawn page (a slide, a Visio page)
// into the order a reader takes them in: from the top down, and from left
// to right among those that start beside each other.
package order

import (
	"sort"

	"github.com/AOShei/go-fast-ooxml/internal/dml"
)

// Item is something to be put in reading order: a shape, or a top-level
// shape or group with everything in it.
type Item struct {
	Box        dml.Rect
	Rank       uint8 // On, Off or Unplaced
	First, End int   // what it stands for: shapes of a unit, or blocks of a shape
}

// The ranks of an item: where it is, as far as order goes.
const (
	On       uint8 = iota // on the page
	Off                   // wholly outside the page
	Unplaced              // where it is is not known
)

// Rank says which of the three an item in the given box is, on a page of
// the given size (zero if not known). boxed says the box is known.
func Rank(box dml.Rect, boxed bool, w, h float64) uint8 {
	switch {
	case !boxed:
		return Unplaced
	case w > 0 && h > 0 && (box.X1 <= 0 || box.Y1 <= 0 || box.X0 >= w || box.Y0 >= h):
		return Off
	}
	return On
}

// Sort sorts items into reading order: from the top down, and from left
// to right among those that start beside each other, which are those
// that start above the middle of the first of them. What lies outside the
// page comes after what is on it. Items whose place is not known come
// last, in the order they are stored.
func Sort(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := &items[i], &items[j]
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		if a.Rank == Unplaced {
			return false
		}
		if a.Box.Y0 != b.Box.Y0 {
			return a.Box.Y0 < b.Box.Y0
		}
		return a.Box.X0 < b.Box.X0
	})
	for i := 0; i < len(items) && items[i].Rank != Unplaced; {
		middle := (items[i].Box.Y0 + items[i].Box.Y1) / 2
		j := i + 1
		for j < len(items) && items[j].Rank == items[i].Rank && items[j].Box.Y0 < middle {
			j++
		}
		if j-i > 1 {
			row := items[i:j]
			sort.SliceStable(row, func(a, b int) bool { return row[a].Box.X0 < row[b].Box.X0 })
		}
		i = j
	}
}
