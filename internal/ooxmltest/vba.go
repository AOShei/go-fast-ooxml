package ooxmltest

import (
	"encoding/binary"
	"math/bits"
	"unicode/utf16"
)

// This file writes VBA projects ([MS-OVBA]) for the tests, on its own from
// the published description, as compound.go writes compound files: the
// library's reading is checked against it, and against projects Office
// wrote (and another program's reading of them).

// VBAModule is a module of a project written by VBAProject.
type VBAModule struct {
	Name string
	Code string // its source, in the project's code page
}

// VBAProject returns a vbaProject.bin holding the modules. Each module's
// stream starts with cache bytes that are not source, as Office's do.
func VBAProject(codePage int, modules ...VBAModule) []byte {
	var dir []byte
	rec := func(id uint16, data []byte) {
		dir = binary.LittleEndian.AppendUint16(dir, id)
		dir = binary.LittleEndian.AppendUint32(dir, uint32(len(data)))
		dir = append(dir, data...)
	}
	u32 := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	u16 := func(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }
	rec(0x0001, u32(1))     // SYSKIND: 32-bit Windows
	rec(0x0002, u32(0x409)) // LCID
	rec(0x0014, u32(0x409)) // LCIDINVOKE
	rec(0x0003, u16(uint16(codePage)))
	rec(0x0004, []byte("VBAProject"))
	rec(0x0005, nil) // DOCSTRING
	rec(0x0040, nil)
	rec(0x0006, nil) // HELPFILEPATH
	rec(0x003D, nil)
	rec(0x0007, u32(0)) // HELPCONTEXT
	rec(0x0008, u32(0)) // LIBFLAGS
	// VERSION: its size field says 4 and six bytes follow.
	dir = binary.LittleEndian.AppendUint16(dir, 0x0009)
	dir = binary.LittleEndian.AppendUint32(dir, 4)
	dir = binary.LittleEndian.AppendUint32(dir, 1)
	dir = binary.LittleEndian.AppendUint16(dir, 0)
	rec(0x000C, nil) // CONSTANTS
	rec(0x003C, nil)
	rec(0x000F, u16(uint16(len(modules))))
	rec(0x0013, u16(0xFFFF)) // COOKIE
	streams := map[string][]byte{}
	cache := []byte("compiled code, not read\x00\x01\x02")
	for _, m := range modules {
		rec(0x0019, []byte(m.Name))
		rec(0x0047, utf16le(m.Name))
		rec(0x001A, []byte(m.Name))
		rec(0x0032, utf16le(m.Name))
		rec(0x001C, nil)
		rec(0x0048, nil)
		rec(0x0031, u32(uint32(len(cache))))
		rec(0x001E, u32(0))
		rec(0x002C, u16(0xFFFF))
		rec(0x0021, nil) // a procedural module
		rec(0x002B, nil)
		streams["VBA/"+m.Name] = append(append([]byte(nil), cache...), VBACompress([]byte(m.Code))...)
	}
	rec(0x0010, nil)
	streams["VBA/dir"] = VBACompress(dir)
	streams["VBA/_VBA_PROJECT"] = []byte{0xCC, 0x61, 0xFF, 0xFF, 0x00, 0x00, 0x00}
	streams["PROJECT"] = []byte("ID=\"{00000000-0000-0000-0000-000000000000}\"\r\n")
	return Compound(streams)
}

func utf16le(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

// VBACompress compresses data as a compressed container ([MS-OVBA]
// 2.4.1), finding the longest match for each copy token.
func VBACompress(data []byte) []byte {
	out := []byte{1}
	for start := 0; start < len(data); start += 4096 {
		chunk := data[start:min(start+4096, len(data))]
		body := compressChunk(chunk)
		if len(chunk) == 4096 && len(body) > 4096 {
			// Stored as it is.
			out = binary.LittleEndian.AppendUint16(out, 0x3FFF)
			out = append(out, chunk...)
		} else {
			out = binary.LittleEndian.AppendUint16(out, uint16(len(body)+2-3)|0xB000)
			out = append(out, body...)
		}
	}
	return out
}

func compressChunk(chunk []byte) []byte {
	var body []byte
	for pos := 0; pos < len(chunk); {
		flags := len(body)
		body = append(body, 0)
		for bit := 0; bit < 8 && pos < len(chunk); bit++ {
			best, offset := 0, 0
			if pos > 0 {
				count := max(bits.Len(uint(pos-1)), 4)
				maxLength := 0xFFFF>>count + 3
				for c := pos - 1; c >= 0 && pos-c <= 1<<(16-count); c-- {
					n := 0
					for n < maxLength && pos+n < len(chunk) && chunk[c+n] == chunk[pos+n] {
						n++
					}
					if n > best {
						best, offset = n, pos-c
					}
				}
				if best >= 3 {
					token := uint16(offset-1)<<(16-count) | uint16(best-3)
					body[flags] |= 1 << bit
					body = binary.LittleEndian.AppendUint16(body, token)
					pos += best
					continue
				}
			}
			body = append(body, chunk[pos])
			pos++
		}
	}
	return body
}
