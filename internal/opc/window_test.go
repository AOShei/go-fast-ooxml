package opc

import (
	"strconv"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// A part read whole is given a window that holds the size it declares,
// in steps of 4 KiB, and no more than the scanner's default.
func TestWindowHoldsThePart(t *testing.T) {
	b := ooxmltest.Docx(ooxmltest.Para("x"))
	for _, n := range []int{100, 4095, 4096, 60 << 10, 64 << 10, 200 << 10} {
		b.AddBytes("custom/part"+strconv.Itoa(n)+".xml", "application/xml", []byte("<x>"+strings.Repeat("y", n-7)+"</x>"))
	}
	p := open(t, b.Bytes(), noLimits)
	spaces := map[string]xmlscan.Space{"urn:x": 1}
	opts := &xmlscan.Options{Spaces: spaces, Recover: true}
	for _, c := range []struct {
		size int64
		want int
	}{{100, 4 << 10}, {4095, 4 << 10}, {4096, 8 << 10}, {60 << 10, 64 << 10}, {64 << 10, 0}, {200 << 10, 0}} {
		var name string
		for _, n := range p.Names() {
			if p.Size(n) == c.size {
				name = n
			}
		}
		if name == "" {
			t.Fatalf("no part of %d bytes", c.size)
		}
		got := p.Window(name, opts)
		if c.want == 0 {
			if got != opts {
				t.Errorf("a part of %d bytes: options changed, window %d", c.size, got.BufferSize)
			}
			continue
		}
		if got.BufferSize != c.want || !got.Recover || len(got.Spaces) != 1 || opts.BufferSize != 0 {
			t.Errorf("a part of %d bytes: window %d, want %d (options %+v)", c.size, got.BufferSize, c.want, got)
		}
	}
	if got := p.Window("no/such/part.xml", opts); got != opts {
		t.Errorf("a missing part: options changed")
	}
	own := &xmlscan.Options{BufferSize: 4 << 10}
	if got := p.Window("word/document.xml", own); got != own {
		t.Errorf("a window asked for was not kept")
	}
	if got := p.Window("word/document.xml", nil); got == nil || got.BufferSize != 4<<10 {
		t.Errorf("no options: %+v", got)
	}
}
