package opc

import (
	"io"
	"sync"

	"github.com/AOShei/go-fast-core/xmlscan"
)

// Counts is what a scanner read as U+FFFD in a part.
type Counts struct {
	// CharRefs: character references to no character XML allows (&#0;,
	// a surrogate, a number past U+10FFFF), xmlscan's
	// Stats.CharRefsReplaced.
	CharRefs int
	// InvalidUTF8: bytes of the text and attribute values read that are
	// not part of a UTF-8 character, xmlscan's Stats.InvalidUTF8.
	InvalidUTF8 int
}

// CountsOf returns what s has read as U+FFFD so far.
func CountsOf(s *xmlscan.Scanner) Counts {
	return Counts{CharRefs: s.Stats.CharRefsReplaced, InvalidUTF8: s.Stats.InvalidUTF8}
}

// Replaced counts what the parts' scanners read as U+FFFD. A part counts
// the most any one reading of it met, so a part read twice is counted
// once. It is safe for concurrent use; its zero value is ready.
type Replaced struct {
	mu sync.Mutex
	n  map[string]Counts
}

// Add counts c in the part or section called name.
func (r *Replaced) Add(name string, c Counts) {
	if r == nil || c.CharRefs <= 0 && c.InvalidUTF8 <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == nil {
		r.n = map[string]Counts{}
	}
	was := r.n[name]
	r.n[name] = Counts{CharRefs: max(was.CharRefs, c.CharRefs), InvalidUTF8: max(was.InvalidUTF8, c.InvalidUTF8)}
}

// Total returns what was counted in all parts.
func (r *Replaced) Total() Counts {
	if r == nil {
		return Counts{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var total Counts
	for _, c := range r.n {
		total.CharRefs += c.CharRefs
		total.InvalidUTF8 += c.InvalidUTF8
	}
	return total
}

// CountReplaced has the parts' scanners count in r what they read as
// U+FFFD, each part when its closer is closed.
func (p *Package) CountReplaced(r *Replaced) { p.replaced = r }

// CountScanned counts what s read as U+FFFD in the part called name, for
// a part a reader opened and scanned itself.
func (p *Package) CountScanned(name string, s *xmlscan.Scanner) {
	p.replaced.Add(Canonical(name), CountsOf(s))
}

// Scan is go-fast-core's Scan, counting what the part's scanner read as
// U+FFFD in the package's Replaced when the closer is closed.
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
	if err != nil || p.replaced == nil {
		return c, err
	}
	return &counted{Closer: c, s: s, replaced: p.replaced, name: Canonical(name)}, nil
}

// counted is a part's closer that counts what its scanner read as U+FFFD
// first.
type counted struct {
	io.Closer
	s        *xmlscan.Scanner
	replaced *Replaced
	name     string
}

func (c *counted) Close() error {
	c.replaced.Add(c.name, CountsOf(c.s))
	return c.Closer.Close()
}
