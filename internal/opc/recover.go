package opc

import (
	"archive/zip"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// A zip archive lists its members in a directory at its end, and a file
// that was cut short in transit has lost it. The members themselves are
// still there, each behind a header of its own, one after another from
// the start of the file. Recovery reads those headers in order, as far as
// they go, and writes the directory the file should have had.
//
// It is tried only when the directory cannot be read, so a sound archive
// is never read this way. It follows the headers from the first byte and
// does not search the file for things that look like headers: what it
// finds is what any reader that goes by the headers would find.

// Recovery says what was found in an archive whose directory could not be
// read.
type Recovery struct {
	// Members is the number of members found by their headers.
	Members int
	// Cut is the name of the last member, if the file ends inside it:
	// its content breaks off.
	Cut string
}

const (
	sigLocal      = 0x04034b50
	sigDirectory  = 0x02014b50
	sigEnd        = 0x06054b50
	sigDescriptor = 0x08074b50

	flagDescriptor = 1 << 3 // the sizes follow the data instead of standing in the header
)

// found is one member found by its header.
type found struct {
	name                    []byte
	flags, method           uint16
	time, date              uint16
	crc, compressed, actual uint32
	header                  uint32 // where its header is
}

var errRecovery = errors.New("no members to recover")

// recoverArchive reads the member headers of an archive without a
// readable directory and returns a reader over the archive with a
// directory made from them.
func recoverArchive(r io.ReaderAt, size int64, lim Limits) (*zip.Reader, *Recovery, error) {
	// What a directory of the plain kind can describe.
	if size >= 1<<32-1 {
		return nil, nil, errRecovery
	}
	budget := lim.MaxDecodedBytes
	if budget <= 0 {
		budget = 1<<63 - 1
	}
	var (
		members []found
		rec     Recovery
		hdr     [30]byte
		src     = &byteSource{r: r}
	)
scan:
	for off := int64(0); off+int64(len(hdr)) <= size; {
		if _, err := r.ReadAt(hdr[:], off); err != nil || binary.LittleEndian.Uint32(hdr[:]) != sigLocal {
			break // the directory begins here, or the file is damaged from here on
		}
		m := found{
			flags:      binary.LittleEndian.Uint16(hdr[6:]),
			method:     binary.LittleEndian.Uint16(hdr[8:]),
			time:       binary.LittleEndian.Uint16(hdr[10:]),
			date:       binary.LittleEndian.Uint16(hdr[12:]),
			crc:        binary.LittleEndian.Uint32(hdr[14:]),
			compressed: binary.LittleEndian.Uint32(hdr[18:]),
			actual:     binary.LittleEndian.Uint32(hdr[22:]),
			header:     uint32(off),
		}
		nameLen, extraLen := int64(binary.LittleEndian.Uint16(hdr[26:])), int64(binary.LittleEndian.Uint16(hdr[28:]))
		data := off + int64(len(hdr)) + nameLen + extraLen
		if nameLen == 0 || data > size {
			break // the file ends inside this header
		}
		m.name = make([]byte, nameLen)
		if _, err := r.ReadAt(m.name, off+int64(len(hdr))); err != nil {
			break
		}
		if lim.MaxMembers > 0 && len(members) >= lim.MaxMembers || len(members) >= 1<<16-1 {
			return nil, nil, ErrTooManyParts
		}
		whole := true
		next := size
		switch {
		case m.compressed == 1<<32-1 || m.actual == 1<<32-1:
			break scan // sizes of the 64-bit kind: not something Office files of this size have
		case m.flags&flagDescriptor == 0:
			// The header says how long the member is.
			if next = data + int64(m.compressed); next > size {
				whole, m.compressed = false, uint32(size-data)
			}
		case m.method != zip.Deflate:
			break scan // stored, with its length after it: nothing says where it ends
		default:
			// The length follows the data. Where the data ends is found
			// by decompressing it, which also gives the length and the
			// checksum the descriptor would have given.
			src.reset(data, size)
			sum := crc32.NewIEEE()
			out := &counter{w: sum, left: budget}
			_, err := io.Copy(out, flate.NewReader(src))
			budget = out.left
			if errors.Is(err, ErrBudget) {
				return nil, nil, ErrBudget
			}
			if out.n >= 1<<32-1 {
				break scan
			}
			whole = err == nil
			m.compressed, m.actual, m.crc = uint32(src.n), uint32(out.n), sum.Sum32()
			if !whole {
				m.crc = 0 // unknown: the member breaks off, and says so when read
			}
			next = descriptorEnd(r, data+src.n, size)
		}
		if !whole && m.compressed == 0 {
			break // nothing of it is there
		}
		m.flags &^= flagDescriptor // the directory gives the sizes
		members = append(members, m)
		if !whole {
			rec.Cut = string(m.name)
			break
		}
		off = next
	}
	if len(members) == 0 {
		return nil, nil, errRecovery
	}
	rec.Members = len(members)

	// The directory, and the record that ends an archive.
	var dir []byte
	for i := range members {
		m := &members[i]
		var e [46]byte
		binary.LittleEndian.PutUint32(e[0:], sigDirectory)
		binary.LittleEndian.PutUint16(e[4:], 20)
		binary.LittleEndian.PutUint16(e[6:], 20)
		binary.LittleEndian.PutUint16(e[8:], m.flags)
		binary.LittleEndian.PutUint16(e[10:], m.method)
		binary.LittleEndian.PutUint16(e[12:], m.time)
		binary.LittleEndian.PutUint16(e[14:], m.date)
		binary.LittleEndian.PutUint32(e[16:], m.crc)
		binary.LittleEndian.PutUint32(e[20:], m.compressed)
		binary.LittleEndian.PutUint32(e[24:], m.actual)
		binary.LittleEndian.PutUint16(e[28:], uint16(len(m.name)))
		binary.LittleEndian.PutUint32(e[42:], m.header)
		dir = append(append(dir, e[:]...), m.name...)
	}
	var end [22]byte
	binary.LittleEndian.PutUint32(end[0:], sigEnd)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(members)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(members)))
	binary.LittleEndian.PutUint32(end[12:], uint32(len(dir)))
	binary.LittleEndian.PutUint32(end[16:], uint32(size))
	tail := append(dir, end[:]...)

	zr, err := zip.NewReader(&withDirectory{r: r, size: size, tail: tail}, size+int64(len(tail)))
	if err != nil {
		return nil, nil, errRecovery
	}
	return zr, &rec, nil
}

// descriptorEnd returns where the next member begins after data that
// ended at off and is followed by its descriptor: twelve bytes, or twenty
// where the sizes are of the 64-bit kind, with or without a signature in
// front. The form is told by what follows it.
func descriptorEnd(r io.ReaderAt, off, size int64) int64 {
	var sig [4]byte
	for _, n := range []int64{16, 12, 24, 20} {
		at := off + n
		if at == size {
			return at
		}
		if at+4 > size {
			continue
		}
		if _, err := r.ReadAt(sig[:], at); err != nil {
			continue
		}
		switch binary.LittleEndian.Uint32(sig[:]) {
		case sigLocal, sigDirectory, sigEnd:
			return at
		}
	}
	return size // the file ends in or right after the descriptor
}

// byteSource reads the bytes of an archive from one place on, one at a
// time, and counts them: the decompressor then takes not a byte more
// than the data it decompresses, and the count is where that data ends.
type byteSource struct {
	r        io.ReaderAt
	off, end int64
	buf      [16 << 10]byte
	lo, hi   int
	n        int64
}

func (b *byteSource) reset(off, end int64) { b.off, b.end, b.lo, b.hi, b.n = off, end, 0, 0, 0 }

func (b *byteSource) ReadByte() (byte, error) {
	if b.lo == b.hi {
		want := min(int64(len(b.buf)), b.end-b.off)
		if want <= 0 {
			return 0, io.EOF
		}
		got, err := b.r.ReadAt(b.buf[:want], b.off)
		if got == 0 {
			if err == nil {
				err = io.ErrNoProgress
			}
			return 0, err
		}
		b.off += int64(got)
		b.lo, b.hi = 0, got
	}
	c := b.buf[b.lo]
	b.lo++
	b.n++
	return c, nil
}

func (b *byteSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c, err := b.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = c
	return 1, nil
}

// counter passes what is decompressed on to a checksum, counts it, and
// stops at the decompression budget.
type counter struct {
	w    io.Writer
	n    int64
	left int64
}

func (c *counter) Write(p []byte) (int, error) {
	if c.left -= int64(len(p)); c.left < 0 {
		return 0, ErrBudget
	}
	c.n += int64(len(p))
	return c.w.Write(p)
}

// withDirectory is an archive with the directory it should have had put
// after its last byte.
type withDirectory struct {
	r    io.ReaderAt
	size int64
	tail []byte
}

func (w *withDirectory) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, errors.New("opc: negative offset")
	}
	if off < w.size {
		want := int(min(int64(len(p)), w.size-off))
		n, err = w.r.ReadAt(p[:want], off)
		if n < want {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
		off += int64(n)
	}
	if n < len(p) {
		at := off - w.size
		if at >= int64(len(w.tail)) {
			return n, io.EOF
		}
		n += copy(p[n:], w.tail[at:])
		if n < len(p) {
			return n, io.EOF
		}
	}
	return n, nil
}
