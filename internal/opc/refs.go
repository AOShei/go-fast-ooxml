package opc

import (
	"io"
	"sync"

	"github.com/AOShei/go-fast-core/xmlscan"
)

// Refs counts the character references to no character XML allows (&#0;,
// a surrogate, a number past U+10FFFF) that the scanners met, which read
// each as U+FFFD (xmlscan's Stats.CharRefsReplaced). A part counts the
// most any one reading of it met, so a part read twice is counted once.
// It is safe for concurrent use; its zero value is ready.
type Refs struct {
	mu sync.Mutex
	n  map[string]int
}

// Add counts n references in the part or section called name.
func (r *Refs) Add(name string, n int) {
	if r == nil || n <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == nil {
		r.n = map[string]int{}
	}
	r.n[name] = max(r.n[name], n)
}

// Total returns the references counted in all parts.
func (r *Refs) Total() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, n := range r.n {
		total += n
	}
	return total
}

// CountRefs has the parts' scanners count their references to no
// character in r, each part when its closer is closed.
func (p *Package) CountRefs(r *Refs) { p.refs = r }

// CountScanned counts the references to no character that s met in the
// part called name, for a part a reader opened and scanned itself.
func (p *Package) CountScanned(name string, s *xmlscan.Scanner) {
	p.refs.Add(Canonical(name), s.Stats.CharRefsReplaced)
}

// Scan is go-fast-core's Scan, counting the part's references to no
// character in the package's Refs when the closer is closed.
func (p *Package) Scan(name string, s *xmlscan.Scanner, opts *xmlscan.Options) (io.Closer, error) {
	c, err := p.Package.Scan(name, s, opts)
	return p.counted(name, s, c, err)
}

// ScanPart is go-fast-core's ScanPart, counting as Scan does.
func (p *Package) ScanPart(name string, s *xmlscan.Scanner, opts *xmlscan.Options, ahead bool) (io.Closer, error) {
	c, err := p.Package.ScanPart(name, s, opts, ahead)
	return p.counted(name, s, c, err)
}

// ScanChunks is go-fast-core's ScanChunks, counting as Scan does.
func (p *Package) ScanChunks(name string, s *xmlscan.Scanner, opts *xmlscan.Options, ahead bool) (io.Closer, error) {
	c, err := p.Package.ScanChunks(name, s, opts, ahead)
	return p.counted(name, s, c, err)
}

func (p *Package) counted(name string, s *xmlscan.Scanner, c io.Closer, err error) (io.Closer, error) {
	if err != nil || p.refs == nil {
		return c, err
	}
	return &counted{Closer: c, s: s, refs: p.refs, name: Canonical(name)}, nil
}

// counted is a part's closer that counts its scanner's references to no
// character first.
type counted struct {
	io.Closer
	s    *xmlscan.Scanner
	refs *Refs
	name string
}

func (c *counted) Close() error {
	c.refs.Add(c.name, c.s.Stats.CharRefsReplaced)
	return c.Closer.Close()
}
