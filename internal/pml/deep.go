package pml

import (
	"errors"

	"github.com/AOShei/go-fast-ooxml/internal/dml"
	"github.com/AOShei/go-fast-ooxml/internal/media"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/theme"
)

// With Options.DeepHidden, text is judged against what lies under it
// where that is not one colour: a gradient, against its stops; a picture
// (a shape's picture fill, a picture under the text, the slide's
// background), against the part of the picture under the text. Text is
// flagged low_contrast if it cannot be told from nearly all of that
// (dml.MostShare). A picture that is tiled, rotated, partly transparent or
// in a format the standard library does not decode is not judged.

// resolveFill turns the relationship of a fill's picture into the name of
// its part, as the page that reads the fill sees it; a picture outside
// the file is none.
func (pg *page) resolveFill(f dml.Fill) dml.Fill {
	if f.Blip == "" {
		return f
	}
	if rel, ok := pg.rels[f.Blip]; ok && !rel.External {
		f.Blip = rel.Target
	} else {
		f.Blip = ""
	}
	return f
}

// samples returns the colours of a fill, painted over area, that lie
// under box; nil where they are not known, and always without
// DeepHidden.
func (pg *page) samples(f dml.Fill, area, box dml.Rect) []theme.Color {
	if !pg.p.opts.DeepHidden || f.Paint != dml.PaintOther {
		return nil
	}
	if len(f.Stops) > 0 {
		colors, ok := pg.pal.Colors(f.Stops)
		if !ok {
			return nil
		}
		return colors
	}
	if f.Blip == "" || f.Tile {
		return nil
	}
	g := pg.p.grid(f.Blip)
	w, h := area.X1-area.X0, area.Y1-area.Y0
	if g == nil || w <= 0 || h <= 0 {
		return nil
	}
	// The part of the picture shown is what the crop leaves.
	l, t, r, b := f.Crop[0], f.Crop[1], f.Crop[2], f.Crop[3]
	sw, shh := 1-l-r, 1-t-b
	if sw <= 0 || shh <= 0 {
		return nil
	}
	u := func(x float64) float64 { return l + (x-area.X0)/w*sw }
	v := func(y float64) float64 { return t + (y-area.Y0)/h*shh }
	return g.Region(u(box.X0), v(box.Y0), u(box.X1), v(box.Y1))
}

// grid returns the picture in the named part reduced to a grid of
// colours, decoding it the first time; nil if it is not judged.
func (p *presentation) grid(name string) *media.Grid {
	p.mu.Lock()
	g, ok := p.grids[name]
	p.mu.Unlock()
	if ok {
		return g
	}
	g, decoded, err := media.Sample(p.pkg, name)
	if errors.Is(err, opc.ErrBudget) {
		p.exceeded.Store(true)
	}
	if !decoded {
		g = nil
	}
	p.mu.Lock()
	if p.grids == nil {
		p.grids = map[string]*media.Grid{}
	}
	p.grids[name] = g
	p.mu.Unlock()
	return g
}
