package sml

import (
	"context"
	"errors"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-ooxml/internal/opc"
	"github.com/AOShei/go-fast-ooxml/internal/unit"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Rows reads the cells of the sheet with the given index a band of rows
// at a time, and hands each band to yield as a part of its own: the same
// kind, number and name as the sheet's, holding the tables that the
// band's cells make. It stops when yield returns false.
//
// What is held at any time is one band, so a sheet of any length is read
// in the memory a band takes. The price is that the sheet's part is read
// twice (once for what applies to all rows: hidden columns, merged cells,
// conditional formatting), that a table which runs over the end of a
// band is cut there, and that only cells are read: the tables a sheet
// declares are not looked up, and comments, shapes and pictures are left
// to the reading of the whole sheet. The limits on cells and text do not
// apply, since nothing accumulates; the decompression budget does.
//
// At least one part is handed on, also for a sheet with no cells: it is
// where the sheet's warnings are.
func (b *Book) Rows(ctx context.Context, index, band int, yield func(gofastcore.Part) bool) *unit.Tally {
	if len(b.refs) == 0 {
		yield(b.Read(ctx, 0).Part)
		return &unit.Tally{}
	}
	// sh.part gathers what is said of the sheet as a whole (it could not
	// be read, it was cut short); each band is a part made from it.
	sh, readable := b.wb.start(ctx, index, b.refs[index], b.budget)
	emitted, stopped := false, false
	hand := func(last bool) bool {
		blocks := sh.arrange(sh.tablesOf(nil))
		// The cells are in the tables now, and the band is done with.
		sh.cells, sh.hiddenRows, sh.unsorted, sh.lastRow = sh.cells[:0], sh.hiddenRows[:0], false, 0
		if len(blocks) == 0 && !last {
			return true
		}
		whole := sh.part
		band := gofastcore.Part{Kind: whole.Kind, Number: whole.Number, Name: whole.Name, Hidden: whole.Hidden, Blocks: blocks}
		if last {
			sh.limits()
			whole = sh.part
			if len(blocks) == 0 && emitted && len(whole.Warnings) == 0 {
				return true // nothing more to hand on, and nothing to say
			}
			band.Warnings = append([]gofastcore.Warning(nil), whole.Warnings...)
		}
		sh.part = band
		if len(blocks) > 0 || !emitted {
			sh.finish(false) // says of an empty sheet that it has no text
		} else {
			sh.part.MarkHidden()
		}
		band, sh.part = sh.part, whole
		sh.noValue, sh.split, sh.removed = 0, 0, 0
		emitted = true
		if !yield(band) {
			stopped = true
		}
		return !stopped
	}
	if readable {
		for _, pass := range []int{passAround, passRows} {
			sh.pass = pass
			if pass == passRows {
				sh.band, sh.emit = max(band, 1), func() bool { return hand(false) }
			}
			c, err := b.wb.pkg.Scan(sh.name, &sh.s, scanOptions)
			if err != nil {
				sh.failed(err)
				break
			}
			sh.read()
			c.Close()
			err = sh.s.Err()
			sh.s = xmlscan.Scanner{}
			if pass == passAround && !errors.Is(err, opc.ErrBudget) {
				// A part that breaks off is met again where it breaks
				// off when its rows are read, and reported then: the
				// rows before that place are still to be had.
				err = nil
			}
			sh.failed(err)
			if err != nil || sh.budget || sh.cut != "" {
				break // what is said about it goes on the last band
			}
		}
	}
	if !stopped {
		hand(true)
	}
	return &sh.result().Tally
}
