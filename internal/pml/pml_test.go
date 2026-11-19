package pml

import (
	"bytes"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/dml"
)

// Whether a picture can hide what is under it is decided from the start
// of its file: only one known to have no transparency does.
func TestOpaqueHeader(t *testing.T) {
	encode := func(img image.Image, format string) []byte {
		var buf bytes.Buffer
		var err error
		switch format {
		case "png":
			err = png.Encode(&buf, img)
		case "jpeg":
			err = jpeg.Encode(&buf, img, nil)
		case "gif":
			err = gif.Encode(&buf, img, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	r := image.Rect(0, 0, 4, 4)
	opaqueRGBA := image.NewRGBA(r)
	for i := 3; i < len(opaqueRGBA.Pix); i += 4 {
		opaqueRGBA.Pix[i] = 255
	}
	paletted := image.NewPaletted(r, color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{0, 0, 0, 0}})
	paletted.Pix[0] = 1
	solidPalette := image.NewPaletted(r, color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 255, 255, 255}})
	for name, c := range map[string]struct {
		data []byte
		want bool
	}{
		"JPEG":                             {encode(opaqueRGBA, "jpeg"), true},
		"grey PNG":                         {encode(image.NewGray(r), "png"), true},
		"opaque colour PNG":                {encode(opaqueRGBA, "png"), true},
		"PNG with an alpha channel":        {encode(image.NewNRGBA(r), "png"), false},
		"PNG with a transparent colour":    {encode(paletted, "png"), false},
		"PNG with a palette, all opaque":   {encode(solidPalette, "png"), true},
		"GIF, which may have transparency": {encode(paletted, "gif"), false},
		"not a picture":                    {[]byte("<svg/>"), false},
		"nothing":                          {nil, false},
		"a PNG cut off in its header":      {encode(opaqueRGBA, "png")[:20], false},
	} {
		if got := media.OpaqueHeader(c.data); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

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
