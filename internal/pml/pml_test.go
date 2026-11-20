package pml

import (
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/dml"
)

// Reading order: from the top down, left to right among shapes that
// start beside each other, what is off the slide after what is on it.
func TestInOrder(t *testing.T) {
	at := func(x0, y0, x1, y1 float64, rank uint8, n int) item {
		return item{box: dml.Rect{X0: x0, Y0: y0, X1: x1, Y1: y1}, rank: rank, first: n}
	}
	items := []item{
		at(400, 300, 600, 340, onSlide, 6),
		at(0, 0, 0, 0, unplaced, 8),
		at(400, 100, 600, 280, onSlide, 3), // a tall right column beside two boxes on the left
		at(50, 110, 250, 150, onSlide, 2),
		at(50, 200, 250, 240, onSlide, 4),
		at(-300, 50, -100, 90, offSlide, 7),
		at(50, 10, 650, 60, onSlide, 1),
		at(50, 305, 250, 345, onSlide, 5),
		at(0, 0, 0, 0, unplaced, 9),
	}
	inOrder(items)
	for i, it := range items {
		if it.first != i+1 {
			var got []int
			for _, it := range items {
				got = append(got, it.first)
			}
			t.Fatalf("order: %v", got)
		}
	}
}
