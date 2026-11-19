// Package cfb reads compound files: the container of the legacy Office
// formats, and of a password-protected Office Open XML file, which is an
// encrypted zip archive stored as a stream inside one.
//
// A compound file is a small file system: storages (directories) and
// streams (files), kept in sectors of fixed size that are chained
// together by a file allocation table. Short streams share sectors of
// their own, smaller kind.
//
// The reader trusts nothing in the file. Every chain is followed for at
// most as many steps as the file has sectors, every count is held against
// the size of the file, and what is kept in memory is a small fraction
// of that size.
package cfb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Errors. Both are wrapped with what was wrong.
var (
	// ErrNotCompound: the input does not begin as a compound file does.
	ErrNotCompound = errors.New("cfb: not a compound file")
	// ErrDamaged: the file's structure contradicts itself or its size.
	ErrDamaged = errors.New("cfb: damaged compound file")
	// ErrNotFound: no stream or storage of that name.
	ErrNotFound = errors.New("cfb: not found")
)

var signature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// Special values in the allocation tables.
const (
	freeSector = 0xFFFFFFFF
	endOfChain = 0xFFFFFFFE
	maxRegular = 0xFFFFFFFA // the largest sector number
	noStream   = 0xFFFFFFFF // no sibling, no child
)

// Kinds of directory entry.
const (
	kindStorage = 1
	kindStream  = 2
	kindRoot    = 5
)

// maxEntries bounds the directory entries read. A file that has more is
// not one this reader is for.
const maxEntries = 1 << 16

// entry is one directory entry.
type entry struct {
	name               string
	kind               byte
	left, right, child uint32
	start              uint32
	size               int64
}

// File is an open compound file.
type File struct {
	r          io.ReaderAt
	size       int64
	shift      uint // of a sector: 9 or 12
	miniShift  uint // of a short-stream sector: 6
	sectors    uint32
	fat        []uint32
	miniFAT    []uint32
	entries    []entry
	miniCutoff int64
	mini       *Stream // the stream that holds the short streams
}

// Open reads the structure of the compound file of the given size in r:
// its allocation tables and its directory. The content of streams is read
// when it is asked for.
func Open(r io.ReaderAt, size int64) (*File, error) {
	var head [512]byte
	if size < int64(len(head)) {
		return nil, fmt.Errorf("%w: %d bytes", ErrNotCompound, size)
	}
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	if string(head[:8]) != string(signature) {
		return nil, ErrNotCompound
	}
	le := binary.LittleEndian
	f := &File{r: r, size: size}
	major := le.Uint16(head[26:])
	f.shift, f.miniShift = uint(le.Uint16(head[30:])), uint(le.Uint16(head[32:]))
	switch {
	case le.Uint16(head[28:]) != 0xFFFE:
		return nil, fmt.Errorf("%w: byte order mark %#x", ErrDamaged, le.Uint16(head[28:]))
	case !(major == 3 && f.shift == 9 || major == 4 && f.shift == 12):
		return nil, fmt.Errorf("%w: version %d with sectors of 2^%d bytes", ErrDamaged, major, f.shift)
	case f.miniShift != 6:
		return nil, fmt.Errorf("%w: short sectors of 2^%d bytes", ErrDamaged, f.miniShift)
	}
	sectorSize := int64(1) << f.shift
	// The first sector follows the header, which fills a sector's space.
	// A last sector that is cut short still counts: some programs do not
	// pad the file.
	whole := (size - sectorSize + sectorSize - 1) >> f.shift
	if whole <= 0 {
		return nil, fmt.Errorf("%w: no sectors", ErrDamaged)
	}
	f.sectors = uint32(min(whole, maxRegular))
	fatSectors := le.Uint32(head[44:])
	firstDir := le.Uint32(head[48:])
	f.miniCutoff = int64(le.Uint32(head[56:]))
	firstMiniFAT, miniFATSectors := le.Uint32(head[60:]), le.Uint32(head[64:])
	firstDIFAT, difatSectors := le.Uint32(head[68:]), le.Uint32(head[72:])
	perSector := uint32(sectorSize / 4)

	// The table has one entry for each sector. Programs set aside more
	// sectors for it than a file needs; only as many are read as have
	// entries for sectors the file has, so that what is kept is a small
	// part of the file's size whatever the header claims.
	if fatSectors == 0 || fatSectors > f.sectors {
		return nil, fmt.Errorf("%w: %d sectors of allocation table in a file of %d sectors", ErrDamaged, fatSectors, f.sectors)
	}
	fatSectors = min(fatSectors, (f.sectors+perSector-1)/perSector)
	// Where the table's own sectors are: the first 109 in the header, the
	// rest in a chain of sectors of their own.
	where := make([]uint32, 0, fatSectors)
	for i := 0; i < 109 && uint32(len(where)) < fatSectors; i++ {
		where = append(where, le.Uint32(head[76+4*i:]))
	}
	buf := make([]byte, sectorSize)
	next := firstDIFAT
	for step := uint32(0); uint32(len(where)) < fatSectors; step++ {
		if next > maxRegular || step >= difatSectors || step > f.sectors {
			return nil, fmt.Errorf("%w: the list of allocation-table sectors ends early", ErrDamaged)
		}
		if err := f.sector(next, buf); err != nil {
			return nil, err
		}
		for i := uint32(0); i < perSector-1 && uint32(len(where)) < fatSectors; i++ {
			where = append(where, le.Uint32(buf[4*i:]))
		}
		next = le.Uint32(buf[sectorSize-4:])
	}
	f.fat = make([]uint32, 0, int(fatSectors)*int(perSector))
	for _, n := range where {
		if err := f.sector(n, buf); err != nil {
			return nil, err
		}
		for i := uint32(0); i < perSector; i++ {
			f.fat = append(f.fat, le.Uint32(buf[4*i:]))
		}
	}

	// The directory.
	dir, err := f.chain(firstDir, -1)
	if err != nil {
		return nil, fmt.Errorf("%w (directory)", err)
	}
	for _, n := range dir {
		if err := f.sector(n, buf); err != nil {
			return nil, err
		}
		for at := int64(0); at+128 <= sectorSize; at += 128 {
			if len(f.entries) >= maxEntries {
				return nil, fmt.Errorf("%w: more than %d directory entries", ErrDamaged, maxEntries)
			}
			raw := buf[at : at+128]
			e := entry{kind: raw[66], left: le.Uint32(raw[68:]), right: le.Uint32(raw[72:]), child: le.Uint32(raw[76:]),
				start: le.Uint32(raw[116:]), size: int64(le.Uint64(raw[120:]))}
			if major == 3 {
				e.size &= 0xFFFFFFFF // the upper half is not kept by this version
			}
			// The name is in UTF-16, with its length in bytes, the
			// terminator included.
			if n := int(le.Uint16(raw[64:])); n >= 2 && n <= 64 && n%2 == 0 {
				units := make([]uint16, n/2-1)
				for i := range units {
					units[i] = le.Uint16(raw[2*i:])
				}
				e.name = string(utf16.Decode(units))
			}
			f.entries = append(f.entries, e)
		}
	}
	if len(f.entries) == 0 || f.entries[0].kind != kindRoot {
		return nil, fmt.Errorf("%w: no root entry", ErrDamaged)
	}

	// Short streams: their sectors are cut from one stream, which the
	// root entry names, and chained by a table of their own.
	root := &f.entries[0]
	if root.size > 0 && root.start <= maxRegular {
		f.mini, err = f.stream(root.start, root.size, false)
		if err != nil {
			return nil, fmt.Errorf("%w (short-stream container)", err)
		}
		table, err := f.chain(firstMiniFAT, int64(miniFATSectors)*sectorSize)
		if err != nil {
			return nil, fmt.Errorf("%w (short-stream table)", err)
		}
		f.miniFAT = make([]uint32, 0, len(table)*int(perSector))
		for _, n := range table {
			if err := f.sector(n, buf); err != nil {
				return nil, err
			}
			for i := uint32(0); i < perSector; i++ {
				f.miniFAT = append(f.miniFAT, le.Uint32(buf[4*i:]))
			}
		}
	}
	return f, nil
}

// sector reads the sector with the given number into buf.
func (f *File) sector(n uint32, buf []byte) error {
	if n >= f.sectors {
		return fmt.Errorf("%w: sector %d of %d", ErrDamaged, n, f.sectors)
	}
	got, err := f.r.ReadAt(buf, (int64(n)+1)<<f.shift)
	if err != nil && !(err == io.EOF && got > 0) {
		return fmt.Errorf("%w: sector %d: %v", ErrDamaged, n, err)
	}
	clear(buf[got:]) // what a short last sector lacks
	return nil
}

// chain returns the sectors of the chain that starts at first. size is
// the number of bytes the chain is to hold, or negative to follow it to
// its end; a chain that ends before it holds that many is returned as
// far as it goes. A chain cannot be longer than the file, so one that
// goes on has looped.
func (f *File) chain(first uint32, size int64) ([]uint32, error) {
	var out []uint32
	want := int64(-1)
	if size >= 0 {
		want = (size + int64(1)<<f.shift - 1) >> f.shift
		if want > int64(f.sectors) {
			return nil, fmt.Errorf("%w: a stream of %d bytes in a file of %d sectors", ErrDamaged, size, f.sectors)
		}
		out = make([]uint32, 0, want)
	}
	for n := first; n != endOfChain && (want < 0 || int64(len(out)) < want); {
		if n >= f.sectors || int64(n) >= int64(len(f.fat)) {
			return nil, fmt.Errorf("%w: a chain leads to sector %d", ErrDamaged, n)
		}
		if uint32(len(out)) >= f.sectors {
			return nil, fmt.Errorf("%w: a chain of sectors loops", ErrDamaged)
		}
		out = append(out, n)
		n = f.fat[n]
	}
	return out, nil
}

// Stream is the content of one stream. It is safe for concurrent use.
type Stream struct {
	f       *File
	size    int64
	shift   uint
	sectors []uint32
	short   bool // its sectors are short ones, cut from the container stream
}

// stream builds the stream that starts at the given sector.
func (f *File) stream(start uint32, size int64, short bool) (*Stream, error) {
	s := &Stream{f: f, size: size, shift: f.shift, short: short}
	if !short {
		var err error
		s.sectors, err = f.chain(start, size)
		// Some programs state a stream longer than the sectors they give
		// it. The stream is then what its sectors hold.
		s.size = min(s.size, int64(len(s.sectors))<<s.shift)
		return s, err
	}
	s.shift = f.miniShift
	if f.mini == nil {
		return nil, fmt.Errorf("%w: a short stream, and no container for short streams", ErrDamaged)
	}
	want := (size + int64(1)<<s.shift - 1) >> s.shift
	if want<<s.shift > f.mini.size+int64(1)<<s.shift {
		return nil, fmt.Errorf("%w: a short stream of %d bytes in a container of %d", ErrDamaged, size, f.mini.size)
	}
	s.sectors = make([]uint32, 0, want)
	for n := start; int64(len(s.sectors)) < want && n != endOfChain; n = f.miniFAT[n] {
		if int64(n) >= int64(len(f.miniFAT)) || int64(n+1)<<s.shift > f.mini.size {
			return nil, fmt.Errorf("%w: a short chain leads to sector %d", ErrDamaged, n)
		}
		s.sectors = append(s.sectors, n)
	}
	s.size = min(s.size, int64(len(s.sectors))<<s.shift)
	return s, nil
}

// Size returns the length of the stream in bytes.
func (s *Stream) Size() int64 { return s.size }

// ReadAt reads from the stream at the given offset, as io.ReaderAt.
func (s *Stream) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("cfb: negative offset")
	}
	n := 0
	for n < len(p) && off < s.size {
		index, within := off>>s.shift, off&(int64(1)<<s.shift-1)
		chunk := min(int64(len(p)-n), int64(1)<<s.shift-within, s.size-off)
		var got int
		var err error
		if s.short {
			got, err = s.f.mini.ReadAt(p[n:n+int(chunk)], int64(s.sectors[index])<<s.shift+within)
		} else {
			got, err = s.f.r.ReadAt(p[n:n+int(chunk)], (int64(s.sectors[index])+1)<<s.shift+within)
		}
		if int64(got) < chunk {
			if err == nil || err == io.EOF {
				err = errors.New("a sector lies beyond the end of the file")
			}
			return n + got, fmt.Errorf("%w: %v", ErrDamaged, err)
		}
		n += int(chunk)
		off += chunk
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// find returns the entry with the given name among the children of the
// entry with the given index. Names are compared without regard to case,
// as the format has it. The children are a tree; a tree that loops is
// walked for no more steps than there are entries.
func (f *File) find(parent int, name string) (int, bool) {
	steps := 0
	var walk func(i uint32) (int, bool)
	walk = func(i uint32) (int, bool) {
		if i == noStream || int64(i) >= int64(len(f.entries)) {
			return 0, false
		}
		if steps++; steps > len(f.entries) {
			return 0, false
		}
		e := &f.entries[i]
		if (e.kind == kindStorage || e.kind == kindStream) && strings.EqualFold(e.name, name) {
			return int(i), true
		}
		if found, ok := walk(e.left); ok {
			return found, true
		}
		return walk(e.right)
	}
	return walk(f.entries[parent].child)
}

// Has reports whether the file has a stream or storage at the given path
// from the root.
func (f *File) Has(path ...string) bool {
	_, ok := f.lookup(path)
	return ok
}

func (f *File) lookup(path []string) (int, bool) {
	at := 0
	for _, name := range path {
		next, ok := f.find(at, name)
		if !ok {
			return 0, false
		}
		at = next
	}
	return at, len(path) > 0
}

// Open returns the stream at the given path from the root: the names of
// the storages it is in, and its own.
func (f *File) Open(path ...string) (*Stream, error) {
	i, ok := f.lookup(path)
	if !ok || f.entries[i].kind != kindStream {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.Join(path, "/"))
	}
	e := &f.entries[i]
	if e.size < 0 || e.size > f.size {
		return nil, fmt.Errorf("%w: a stream of %d bytes in a file of %d", ErrDamaged, e.size, f.size)
	}
	if e.size == 0 {
		return &Stream{f: f, shift: f.shift}, nil
	}
	return f.stream(e.start, e.size, e.size < f.miniCutoff)
}

// Names returns the names of the streams and storages directly in the
// root, in no particular order.
func (f *File) Names() []string {
	var out []string
	steps := 0
	var walk func(i uint32)
	walk = func(i uint32) {
		if i == noStream || int64(i) >= int64(len(f.entries)) {
			return
		}
		if steps++; steps > len(f.entries) {
			return
		}
		e := &f.entries[i]
		if e.kind == kindStorage || e.kind == kindStream {
			out = append(out, e.name)
		}
		walk(e.left)
		walk(e.right)
	}
	walk(f.entries[0].child)
	return out
}
