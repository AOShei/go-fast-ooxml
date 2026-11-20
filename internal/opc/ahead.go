package opc

import (
	"io"

	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
)

// Sizes for reading a part in chunks: the chunk, and how many chunks a
// read-ahead may hold ready.
const (
	chunkSize  = 256 << 10
	aheadDepth = 4
)

// ScanChunks is Scan for a part read in chunks of a fixed size, the
// budget charged a chunk at a time as the scanner takes it. With ahead
// set, the chunks are decompressed on a goroutine of their own, a few
// ahead of the scanner, for a reader that has a worker to spare.
//
// The scanner is handed the same bytes, in the same pieces, and the
// budget charged the same, either way: what a part gives, and where a
// limit cuts it, do not depend on whether it was read ahead. Not where
// it is read through Scan: Scan charges each read as it comes.
func (p *Package) ScanChunks(name string, s *xmlscan.Scanner, opts *xmlscan.Options, ahead bool) (io.Closer, error) {
	rc, err := p.openRaw(name)
	if err != nil {
		return nil, err
	}
	c := &chunks{rc: rc, p: p}
	if ahead {
		c.full, c.free, c.quit, c.done = make(chan chunk, aheadDepth), make(chan []byte, aheadDepth+1), make(chan struct{}), make(chan struct{})
		for range aheadDepth + 1 {
			c.free <- make([]byte, chunkSize)
		}
		go c.fill()
	} else {
		c.buf = make([]byte, chunkSize)
	}
	s.Reset(c, opts)
	return c, nil
}

type chunk struct {
	b   []byte
	err error
}

// chunks hands a part to its reader a chunk at a time, read in place
// (buf) or by a goroutine ahead of it (full, free).
type chunks struct {
	rc  io.ReadCloser
	p   *Package
	buf []byte

	full chan chunk
	free chan []byte
	quit chan struct{}
	done chan struct{}

	cur chunk
	off int
	err error // what comes once the current chunk is used up
}

// read reads one chunk from the part into buf.
func read(rc io.Reader, buf []byte) chunk {
	n, err := io.ReadFull(rc, buf)
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	return chunk{buf[:n], err}
}

// fill reads chunks ahead until the end of the part, an error, or Close.
func (c *chunks) fill() {
	defer close(c.done)
	for {
		var buf []byte
		select {
		case buf = <-c.free:
		case <-c.quit:
			return
		}
		ch := read(c.rc, buf)
		select {
		case c.full <- ch:
		case <-c.quit:
			return
		}
		if ch.err != nil {
			return
		}
	}
}

// next takes the next chunk and charges the budget for it. A chunk the
// budget has no room for all of is cut to what it has room for.
func (c *chunks) next() {
	if c.full != nil {
		if c.cur.b != nil {
			c.free <- c.cur.b[:cap(c.cur.b)]
		}
		c.cur = <-c.full
	} else {
		c.cur = read(c.rc, c.buf)
	}
	c.off = 0
	if n := len(c.cur.b); n > 0 {
		if left := c.p.budget.Add(-int64(n)); left < 0 {
			c.cur.b = c.cur.b[:max(int64(n)+left, 0)]
			c.cur.err = ErrBudget
		}
	}
	c.err = c.cur.err
}

func (c *chunks) Read(b []byte) (int, error) {
	for c.off == len(c.cur.b) {
		if c.err != nil {
			return 0, c.err
		}
		c.next()
	}
	n := copy(b, c.cur.b[c.off:])
	c.off += n
	if c.off == len(c.cur.b) && c.err != nil {
		return n, c.err
	}
	return n, nil
}

// Close stops the read-ahead, if there is one, and closes the part.
func (c *chunks) Close() error {
	if c.quit != nil {
		close(c.quit)
		<-c.done
	}
	return c.rc.Close()
}
