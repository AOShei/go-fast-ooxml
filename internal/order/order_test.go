package order

import (
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/dml"
)

// Reading order: from the top down, left to right among shapes that
// start beside each other, what is off the page after what is on it.
func TestSort(t *testing.T) {
	at := func(x0, y0, x1, y1 float64, rank uint8, n int) Item {
		return Item{Box: dml.Rect{X0: x0, Y0: y0, X1: x1, Y1: y1}, Rank: rank, First: n}
	}
	items := []Item{
		at(400, 300, 600, 340, On, 6),
		at(0, 0, 0, 0, Unplaced, 8),
		at(400, 100, 600, 280, On, 3), // a tall right column beside two boxes on the left
		at(50, 110, 250, 150, On, 2),
		at(50, 200, 250, 240, On, 4),
		at(-300, 50, -100, 90, Off, 7),
		at(50, 10, 650, 60, On, 1),
		at(50, 305, 250, 345, On, 5),
		at(0, 0, 0, 0, Unplaced, 9),
	}
	Sort(items)
	for i, it := range items {
		if it.First != i+1 {
			var got []int
			for _, it := range items {
				got = append(got, it.First)
			}
			t.Fatalf("order: %v", got)
		}
	}
}
