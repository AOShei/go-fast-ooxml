package xmlscan

import (
	"encoding/binary"
	"os"
	"testing"
)

// BenchmarkReal scans the parts in the file XMLSCAN_PARTS names: each a
// 4-byte little-endian length and the part's bytes. It reads every token,
// its local name or text, and two attributes by name, as the readers
// look them up.
func BenchmarkReal(b *testing.B) {
	path := os.Getenv("XMLSCAN_PARTS")
	if path == "" {
		b.Skip("XMLSCAN_PARTS not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	var parts [][]byte
	for len(data) >= 4 {
		n := binary.LittleEndian.Uint32(data)
		parts = append(parts, data[4:4+n])
		data = data[4+n:]
	}
	opts := &Options{Spaces: benchSpaces}
	s := NewBytes(nil, opts)
	b.SetBytes(size(parts))
	var sink int
	for b.Loop() {
		for _, p := range parts {
			s.ResetBytes(p, opts)
			for {
				k := s.Next()
				if k == EOF {
					break
				}
				switch k {
				case Start:
					sink += len(s.Local()) + int(s.Space())
					// As the readers do: an attribute or two by name.
					if v, ok := s.Attr(s.Space(), "val"); ok {
						sink += len(v)
					}
					if v, ok := s.Attr(NoSpace, "r"); ok {
						sink += len(v)
					}
				case Text:
					sink += len(s.Text())
				}
			}
		}
	}
	_ = sink
}
