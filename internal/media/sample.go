package media

import (
	"bytes"
	"image"
	"io"

	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
)

// What text is seen against may be a picture. To judge whether text can
// be seen on it, a picture is decoded (PNG, JPEG and GIF, with the
// standard library; other formats are not judged) and reduced to a grid of
// colours, each the colour of the pixel at the middle of its cell. Text is
// then compared with the cells under it.

// Grid is a picture reduced to a grid of colours.
type Grid struct {
	W, H   int
	Colors []theme.Color // row by row
}

// Sizes of a grid, and the largest picture decoded for one.
const (
	gridSize        = 64
	maxSampledPixel = 40 << 20
	maxSampledBytes = 64 << 20
)

// Sample decodes the picture in the named part into a grid. ok is false
// if it cannot be decoded, is larger than is decoded for this, or has
// any transparency where the grid samples it: what shows through is not
// known. The error is opc.ErrBudget if the decompression budget ran out.
func Sample(pkg *opc.Package, name string) (g *Grid, ok bool, err error) {
	rc, err := pkg.Open(name)
	if err != nil {
		return nil, false, budget(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxSampledBytes+1))
	if err != nil {
		return nil, false, budget(err)
	}
	if len(data) > maxSampledBytes {
		return nil, false, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxSampledPixel {
		return nil, false, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, nil
	}
	b := img.Bounds()
	w, h := min(gridSize, b.Dx()), min(gridSize, b.Dy())
	g = &Grid{W: w, H: h, Colors: make([]theme.Color, 0, w*h)}
	for y := 0; y < h; y++ {
		py := b.Min.Y + (2*y+1)*b.Dy()/(2*h)
		for x := 0; x < w; x++ {
			px := b.Min.X + (2*x+1)*b.Dx()/(2*w)
			r, gg, bl, a := img.At(px, py).RGBA()
			if a != 0xFFFF {
				return nil, false, nil
			}
			g.Colors = append(g.Colors, theme.Color{R: uint8(r >> 8), G: uint8(gg >> 8), B: uint8(bl >> 8)})
		}
	}
	return g, true, nil
}

// Region returns the colours of the cells whose middles lie in the part
// of the picture from (u0, v0) to (u1, v1), as shares of its width and
// height. A part smaller than a cell gives the cell it is in.
func (g *Grid) Region(u0, v0, u1, v1 float64) []theme.Color {
	clamp := func(v float64) float64 { return min(max(v, 0), 1) }
	u0, v0, u1, v1 = clamp(u0), clamp(v0), clamp(u1), clamp(v1)
	var out []theme.Color
	for y := 0; y < g.H; y++ {
		cy := (float64(y) + 0.5) / float64(g.H)
		if cy < v0 || cy > v1 {
			continue
		}
		for x := 0; x < g.W; x++ {
			cx := (float64(x) + 0.5) / float64(g.W)
			if cx >= u0 && cx <= u1 {
				out = append(out, g.Colors[y*g.W+x])
			}
		}
	}
	if len(out) == 0 && u1 >= u0 && v1 >= v0 {
		x := min(int((u0+u1)/2*float64(g.W)), g.W-1)
		y := min(int((v0+v1)/2*float64(g.H)), g.H-1)
		out = append(out, g.Colors[y*g.W+x])
	}
	return out
}
