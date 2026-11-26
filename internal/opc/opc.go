// Package opc reads the container of an Office Open XML file: a zip
// archive holding parts, a table of their content types, and relationship
// parts that link them; or the same package written as one XML document
// (Flat OPC, flat.go).
//
// It is the only place the library touches the archive, so it is where the
// limits on decompression are enforced.
package opc

import (
	"archive/zip"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/AOShei/go-fast-core/ziprecover"
	"github.com/AOShei/go-fast-ooxml/internal/clean"
	"github.com/AOShei/go-fast-ooxml/internal/ns"
	"github.com/AOShei/go-fast-ooxml/internal/warn"
	"github.com/AOShei/go-fast-ooxml/internal/xmlscan"
	"sync"
)

// Errors returned by this package. The extractor maps them to the shared
// gofastcore errors.
var (
	// ErrNotZip: the input is not a zip archive.
	ErrNotZip = errors.New("opc: not a zip archive")
	// ErrCutOff: the input begins as a zip archive does, the directory at
	// its end cannot be read (the file was cut short, or damaged there),
	// and what the members' own headers lead to is not enough to read a
	// document from.
	ErrCutOff = errors.New("opc: zip archive whose directory cannot be read (the file is cut short or damaged), with too little left to read a document from")
	// ErrNotPackage: a zip archive with no content-types table, so some
	// other zip-based format.
	ErrNotPackage = errors.New("opc: zip archive is not an Office Open XML package")
	// ErrDamaged: a package whose own structure cannot be read.
	ErrDamaged = errors.New("opc: damaged package")
	// ErrNoMainPart: a package with no Word, Excel, PowerPoint or Visio
	// main part.
	ErrNoMainPart = errors.New("opc: package holds no Word, Excel, PowerPoint or Visio document")
	// ErrNotFound: the named part is not in the package.
	ErrNotFound = errors.New("opc: no such part")
	// ErrBudget: reading went over Limits.MaxDecodedBytes.
	ErrBudget = errors.New("opc: decompressed size limit reached")
	// ErrTooManyParts: the archive has more members than Limits.MaxMembers.
	ErrTooManyParts = errors.New("opc: too many parts")
	// ErrEncryptedMember: a zip member is encrypted with zip encryption.
	ErrEncryptedMember = errors.New("opc: zip member is encrypted")
)

// Limits bounds what a package may cost. Values are final: the caller has
// already applied its defaults.
type Limits struct {
	// MaxDecodedBytes is the total number of decompressed bytes that may
	// be read from the package, over all parts.
	MaxDecodedBytes int64
	// MaxMembers is the largest number of zip members accepted.
	MaxMembers int
}

// Rel is one relationship of a part or of the package.
type Rel struct {
	ID   string
	Type string
	// Target is a part name in canonical form, or, when External is set,
	// the URL or path as written, made fit to appear in the output
	// (clean.Address). It is cleaned here, once: every link to it then
	// holds the same string.
	Target   string
	External bool
}

// Package is an opened Office Open XML package. It is safe for concurrent
// use.
type Package struct {
	files     map[string]*zip.File // by canonical part name
	written   map[string]string    // canonical name -> the name as the archive writes it, where they differ
	names     []string             // canonical names, in archive order
	defaults  map[string]string    // lower-case extension -> content type
	overrides map[string]string    // canonical part name -> content type
	budget    atomic.Int64         // decompressed bytes still allowed
	ambiguous int                  // members that different readers would read differently
	recovered *ziprecover.Recovery // set if the directory was rebuilt from the members' headers
	trailing  []byte               // what follows the end of the archive, up to maxTrailing
	afterEnd  int64                // how many bytes follow it in all

	flat       bool                 // a Flat OPC package: its parts are flatParts, not files
	flatParts  map[string]*flatPart // by canonical part name
	flatDamage *FlatDamage          // where a Flat OPC package breaks off
	sections   bool                 // the parts are elements of a file with no package (NewSections)
	untyped    bool                 // a Visio package with no content-types table, known by its relationships

	cutMu    sync.Mutex
	cut      map[string]int64 // by relationships part: external targets cut to their bound
	cutNames map[string]int64 // names of parts and their types put in the output, cut: the bytes left out
}

// Label is clean.Label for a name of a part, or its content type, that is
// put in the output (a picture's identifier, an embedded file's name): a
// name cut to its bound is counted, once however often it is used.
func (p *Package) Label(s string) string {
	if n := len(strings.TrimSpace(s)); n > warn.MaxName {
		p.cutMu.Lock()
		if p.cutNames == nil {
			p.cutNames = map[string]int64{}
		}
		p.cutNames[s] = int64(n - warn.MaxName)
		p.cutMu.Unlock()
	}
	return clean.Label(s)
}

// CutNames returns how many names Label cut, and the bytes it left out.
func (p *Package) CutNames() (n, bytes int64) {
	p.cutMu.Lock()
	defer p.cutMu.Unlock()
	for _, b := range p.cutNames {
		n, bytes = n+1, bytes+b
	}
	return n, bytes
}

// cutTargets records that the relationships part name has n external
// targets longer than an address is kept. A part read again says the
// same, and is counted once.
func (p *Package) cutTargets(name string, n int64) {
	if n == 0 {
		return
	}
	p.cutMu.Lock()
	defer p.cutMu.Unlock()
	if p.cut == nil {
		p.cut = map[string]int64{}
	}
	p.cut[name] = max(p.cut[name], n)
}

// CutTargets returns how many external targets of the relationships read
// so far were cut to the length an address is kept to.
func (p *Package) CutTargets() int64 {
	p.cutMu.Lock()
	defer p.cutMu.Unlock()
	n := int64(0)
	for _, k := range p.cut {
		n += k
	}
	return n
}

// Open reads the archive's directory and the content-types table.
func Open(r io.ReaderAt, size int64, lim Limits) (*Package, error) {
	// A file whose directory cannot be read but which begins with a
	// member's header is recovered by go-fast-core's ziprecover, which
	// finds the members by their own headers. It registers go-fast-core's
	// decompressor, which takes about half the time of compress/flate and
	// gives the same bytes; archive/zip still checks each part's CRC-32.
	zr, recovered, err := ziprecover.Open(r, size, ziprecover.Limits{MaxMembers: lim.MaxMembers, MaxDecodedBytes: lim.MaxDecodedBytes})
	switch {
	case errors.Is(err, ziprecover.ErrNotZip):
		return nil, fmt.Errorf("%w: %v", ErrNotZip, err)
	case errors.Is(err, ziprecover.ErrBudget):
		return nil, ErrBudget
	case errors.Is(err, ziprecover.ErrTooManyMembers):
		return nil, ErrTooManyParts
	case err != nil:
		return nil, ErrCutOff
	}
	if lim.MaxMembers > 0 && len(zr.File) > lim.MaxMembers {
		return nil, fmt.Errorf("%w: %d, limit %d", ErrTooManyParts, len(zr.File), lim.MaxMembers)
	}
	p := &Package{
		files:     make(map[string]*zip.File, len(zr.File)),
		names:     make([]string, 0, len(zr.File)),
		defaults:  map[string]string{},
		overrides: map[string]string{},
		recovered: recovered,
	}
	p.budget.Store(lim.MaxDecodedBytes)
	if recovered == nil {
		p.trailing, p.afterEnd = trailing(r, size)
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue // a directory entry
		}
		name := Canonical(f.Name)
		if other := unicodePath(f); other != "" && Canonical(other) != name {
			p.ambiguous++ // a second name for this member; it is read under its first
		}
		if _, dup := p.files[name]; dup {
			p.ambiguous++
			continue // the first of two members with one name is the part
		}
		p.files[name] = f
		p.names = append(p.names, name)
		if shown := strings.TrimLeft(strings.ReplaceAll(f.Name, "\\", "/"), "/"); shown != name {
			if p.written == nil {
				p.written = map[string]string{}
			}
			p.written[name] = shown
		}
	}
	if _, ok := p.files[contentTypesName]; !ok && recovered != nil {
		// Some programs write the table of content types last, and it is
		// lost with the end of the file. The document is then known by
		// where its main part is (Main), and the parts by the
		// relationships that lead to them.
		return p, nil
	}
	if _, ok := p.files[contentTypesName]; !ok && p.visioWithoutTypes() {
		// Some programs that write Visio drawings leave the table out.
		// The package's relationships still lead to the drawing, and the
		// parts are known by the relationships that lead to them.
		p.untyped = true
		return p, nil
	}
	if _, ok := p.files[contentTypesName]; !ok {
		// Other formats kept in a zip archive (OpenDocument, EPUB) say
		// what they are in a member of their own.
		if f, ok := p.files[Canonical("mimetype")]; ok && f.UncompressedSize64 <= 256 {
			if rc, err := f.Open(); err == nil {
				said, _ := io.ReadAll(io.LimitReader(rc, 256))
				rc.Close()
				if kind := strings.TrimSpace(string(said)); strings.HasPrefix(kind, "application/") {
					return nil, fmt.Errorf("%w: it is %s", ErrNotPackage, kind)
				}
			}
		}
		return nil, ErrNotPackage
	}
	if err := p.readContentTypes(); err != nil {
		if recovered != nil && !errors.Is(err, ErrBudget) {
			return p, nil // the table breaks off: what it said before that is used
		}
		return nil, err
	}
	return p, nil
}

// visioWithoutTypes reports whether the package's relationships lead to
// a Visio drawing that is in the archive: what makes a zip archive with
// no table of content types a Visio package.
func (p *Package) visioWithoutTypes() bool {
	rels, err := p.Rels("")
	if err != nil {
		return false
	}
	for _, r := range rels {
		if !r.External && r.Type == RelVisioDocument && p.Has(r.Target) {
			return true
		}
	}
	return false
}

// Untyped reports whether the package is a Visio package with no table of
// content types, read by its relationships.
func (p *Package) Untyped() bool { return p.untyped }

// Trailing returns the bytes that follow the end of the archive (its
// end-of-directory record and the comment it states), as many as
// maxTrailing of them, and how many follow it in all; nil and 0 for none.
// Office stops reading there; the bytes are data appended to the file.
func (p *Package) Trailing() (data []byte, total int64) { return p.trailing, p.afterEnd }

// maxTrailing bounds what is read of the data after the archive's end.
const maxTrailing = warn.MaxTrailing

// trailing finds the archive's end-of-directory record as archive/zip
// does, the last one in the final 64 KiB whose comment fits in the file,
// and returns what follows the record and its comment.
func trailing(r io.ReaderAt, size int64) ([]byte, int64) {
	const record = 22 // the record without its comment
	window := min(size, record+0xFFFF+1024)
	if window < record {
		return nil, 0
	}
	buf := make([]byte, window)
	if n, err := r.ReadAt(buf, size-window); int64(n) != window && err != nil {
		return nil, 0
	}
	for i := len(buf) - record; i >= 0; i-- {
		if buf[i] != 'P' || buf[i+1] != 'K' || buf[i+2] != 5 || buf[i+3] != 6 {
			continue
		}
		comment := int(buf[i+20]) | int(buf[i+21])<<8
		end := i + record + comment
		if end > len(buf) {
			return nil, 0 // a comment longer than the file: archive/zip refuses it too
		}
		total := int64(len(buf) - end)
		if total == 0 {
			return nil, 0
		}
		return append([]byte(nil), buf[end:end+int(min(total, maxTrailing))]...), total
	}
	return nil, 0
}

// Recovered returns what was found by the members' own headers, if the
// archive had no directory that could be read; nil for a sound archive.
func (p *Package) Recovered() *ziprecover.Recovery { return p.recovered }

const contentTypesName = "[content_types].xml"

// Canonical returns the form of a part name used as a key: no leading
// slash, forward slashes, percent-escapes decoded, ASCII letters in lower
// case. Part names are case-insensitive, and producers disagree on the
// rest.
func Canonical(name string) string {
	plain := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '\\' || c == '%' || (c >= 'A' && c <= 'Z') || (i == 0 && c == '/') {
			plain = false
			break
		}
	}
	if plain {
		return name
	}
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '\\':
			c = '/'
		case c == '%' && i+2 < len(name) && isHex(name[i+1]) && isHex(name[i+2]):
			c = unhex(name[i+1])<<4 | unhex(name[i+2])
			i += 2
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	for len(b) > 0 && b[0] == '/' {
		b = b[1:]
	}
	return string(b)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// Resolve returns the canonical name of the part that target refers to
// when written in a relationship of the part named source ("" for the
// package). A target that starts with a slash is relative to the package
// root; any other is relative to the directory of source.
func Resolve(source, target string) string {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i] // a fragment addresses a place inside the part
	}
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "\\") {
		return Canonical(path.Clean(strings.ReplaceAll(target, "\\", "/")))
	}
	dir := path.Dir(source)
	if source == "" || dir == "." {
		dir = ""
	}
	joined := path.Clean(path.Join(dir, strings.ReplaceAll(target, "\\", "/")))
	// A target that climbs out of the package stays at its root.
	for strings.HasPrefix(joined, "../") {
		joined = joined[3:]
	}
	if joined == ".." || joined == "." {
		joined = ""
	}
	return Canonical(joined)
}

// Ambiguous returns the number of archive members that programs disagree
// about: a member whose name is given twice, differently (once in its
// header and once in a Unicode-path field), and a member with the same name
// as an earlier one. Such an archive shows one document to one reader and
// another to the next. This package reads each member under its header
// name and takes the first of two with one name.
func (p *Package) Ambiguous() int { return p.ambiguous }

// unicodePath returns the name a member's Info-ZIP Unicode Path extra
// field gives it, or "" if it has no valid one.
func unicodePath(f *zip.File) string {
	extra := f.Extra
	for len(extra) >= 4 {
		id := uint16(extra[0]) | uint16(extra[1])<<8
		size := int(extra[2]) | int(extra[3])<<8
		if size > len(extra)-4 {
			return ""
		}
		field := extra[4 : 4+size]
		extra = extra[4+size:]
		if id != 0x7075 || len(field) < 5 || field[0] != 1 {
			continue
		}
		// The field is only valid for the header name it was written for.
		sum := uint32(field[1]) | uint32(field[2])<<8 | uint32(field[3])<<16 | uint32(field[4])<<24
		if crc32.ChecksumIEEE([]byte(f.Name)) != sum {
			return ""
		}
		return string(field[5:])
	}
	return ""
}

// Has reports whether the package has the named part.
func (p *Package) Has(name string) bool {
	if p.flat {
		_, ok := p.flatParts[Canonical(name)]
		return ok
	}
	_, ok := p.files[Canonical(name)]
	return ok
}

// Written returns a part's name as the archive writes it, with its own
// capitals. Part names are matched without regard to case, but a name
// shown to a person, or used as an identifier in the output, should be the
// file's own.
func (p *Package) Written(name string) string {
	name = Canonical(name)
	if shown, ok := p.written[name]; ok {
		return shown
	}
	return name
}

// Names returns the canonical names of all parts, in archive order. The
// slice must not be modified.
func (p *Package) Names() []string { return p.names }

// Size returns the uncompressed size the archive declares for a part, or
// -1 if there is no such part. A hostile archive can declare anything.
func (p *Package) Size(name string) int64 {
	if p.flat {
		m, ok := p.flatParts[Canonical(name)]
		if !ok {
			return -1
		}
		return m.size()
	}
	f, ok := p.files[Canonical(name)]
	if !ok {
		return -1
	}
	return int64(f.UncompressedSize64)
}

// ContentType returns the content type of a part: its override if it has
// one, else the default for its extension, else "".
func (p *Package) ContentType(name string) string {
	name = Canonical(name)
	if ct, ok := p.overrides[name]; ok {
		return ct
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return p.defaults[name[i+1:]]
	}
	return ""
}

// HasContentType reports whether any part is declared with the given
// content type.
func (p *Package) HasContentType(ct string) bool {
	for _, v := range p.overrides {
		if v == ct {
			return true
		}
	}
	for ext, v := range p.defaults {
		if v != ct {
			continue
		}
		for _, n := range p.names {
			if strings.HasSuffix(n, "."+ext) {
				return true
			}
		}
	}
	return false
}

// Remaining returns how many decompressed bytes may still be read.
func (p *Package) Remaining() int64 { return max(p.budget.Load(), 0) }

// Budget returns the decompression budget as it stands, and Restore puts
// it back to a value Budget returned. They are for a reader that has to
// read a stretch of the package again and must find the limit where it
// found it the first time.
func (p *Package) Budget() int64 { return p.budget.Load() }

// Restore sets the decompression budget. See Budget.
func (p *Package) Restore(budget int64) { p.budget.Store(budget) }

// Charge takes n bytes from the decompression budget, for what a reader
// decompresses itself from a part it has read (the code of a VBA project).
// It reports false if the budget has run out.
func (p *Package) Charge(n int64) bool { return p.budget.Add(-n) >= 0 }

// Open returns a reader for a part's decompressed content. Every byte read
// is charged to the package's budget, and a read that goes over it fails
// with ErrBudget, so no part can expand without bound however small it is
// in the archive.
func (p *Package) Open(name string) (io.ReadCloser, error) {
	rc, err := p.openRaw(name)
	if err != nil {
		return nil, err
	}
	return &budgetReader{rc: rc, p: p}, nil
}

// openRaw opens a member, decompressing it, without charging the budget.
func (p *Package) openRaw(name string) (io.ReadCloser, error) {
	if p.flat {
		m, ok := p.flatParts[Canonical(name)]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return m.open(), nil
	}
	f, ok := p.files[Canonical(name)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if f.Flags&1 != 0 {
		return nil, fmt.Errorf("%w: %s", ErrEncryptedMember, name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, name, err)
	}
	return rc, nil
}

type budgetReader struct {
	rc io.ReadCloser
	p  *Package
}

// Read hands on what the budget has room for and no more: where a part
// is cut by the budget does not depend on how much each read asks for.
func (b *budgetReader) Read(buf []byte) (int, error) {
	n, err := b.rc.Read(buf)
	if n > 0 {
		if left := b.p.budget.Add(-int64(n)); left < 0 {
			return max(n+int(max(left, -int64(n))), 0), ErrBudget
		}
	}
	return n, err
}

func (b *budgetReader) Close() error { return b.rc.Close() }

// Scan opens a part and returns a scanner over it. The caller closes the
// returned closer when done.
func (p *Package) Scan(name string, s *xmlscan.Scanner, opts *xmlscan.Options) (io.Closer, error) {
	rc, err := p.Open(name)
	if err != nil {
		return nil, err
	}
	s.Reset(rc, p.Window(name, opts))
	return released{rc, s}, nil
}

// released closes a part's reader and hands its scanner's window back to
// be reused: the caller closes it when it has read the part.
type released struct {
	io.Closer
	s *xmlscan.Scanner
}

func (r released) Close() error {
	r.s.Release()
	return r.Closer.Close()
}

// Window returns opts with a window for a part read whole: one that holds
// the size the archive declares for it, in steps of 4 KiB, up to the
// scanner's default. A part of a kilobyte needs no 64 KiB window, and
// most parts of a document are a few kilobytes. Text longer than the
// window is handed over in pieces, so a part larger than it declares is
// read as it is with any window. It is Options as well.
func (p *Package) Window(name string, opts *xmlscan.Options) *xmlscan.Options {
	opts = p.Options(name, opts)
	size := p.Size(name)
	if size < 0 || size >= xmlscan.DefaultBufferSize || (opts != nil && opts.BufferSize > 0) {
		return opts
	}
	var o xmlscan.Options
	if opts != nil {
		o = *opts
	}
	o.BufferSize = int(size+1+4095) &^ 4095
	return &o
}

// Options returns the options to scan a part with: opts, with the
// namespaces declared around the part when it is the XML of a Flat OPC
// package's part.
func (p *Package) Options(name string, opts *xmlscan.Options) *xmlscan.Options {
	if !p.flat {
		return opts
	}
	m, ok := p.flatParts[Canonical(name)]
	if !ok || len(m.outer)+len(m.own) == 0 {
		return opts
	}
	var o xmlscan.Options
	if opts != nil {
		o = *opts
	}
	o.Bindings = slices.Concat(m.outer, m.own, o.Bindings)
	return &o
}

var scanOptions = &xmlscan.Options{Spaces: ns.Spaces}

func (p *Package) readContentTypes() error {
	var s xmlscan.Scanner
	c, err := p.Scan(contentTypesName, &s, scanOptions)
	if err != nil {
		return err
	}
	defer c.Close()
	for {
		switch s.Next() {
		case xmlscan.Start:
			switch {
			case s.Is(ns.ContentTypes, "Default"):
				ext, _ := s.Attr(xmlscan.NoSpace, "Extension")
				ct, _ := s.Attr(xmlscan.NoSpace, "ContentType")
				if len(ext) > 0 {
					p.defaults[strings.ToLower(string(ext))] = string(ct)
				}
			case s.Is(ns.ContentTypes, "Override"):
				part, _ := s.Attr(xmlscan.NoSpace, "PartName")
				ct, _ := s.Attr(xmlscan.NoSpace, "ContentType")
				if len(part) > 0 {
					p.overrides[Canonical(string(part))] = string(ct)
				}
			}
		case xmlscan.EOF:
			if err := s.Err(); err != nil {
				if errors.Is(err, ErrBudget) {
					return err
				}
				return fmt.Errorf("%w: content types: %v", ErrDamaged, err)
			}
			return nil
		}
	}
}

// RelsName returns the name of the relationships part of source ("" for
// the package).
func RelsName(source string) string {
	if source == "" {
		return "_rels/.rels"
	}
	source = Canonical(source)
	return path.Join(path.Dir(source), "_rels", path.Base(source)+".rels")
}

// Rels returns the relationships of the part named source, or of the
// package when source is "". A part without a relationships part has none,
// which is not an error.
func (p *Package) Rels(source string) ([]Rel, error) {
	var s xmlscan.Scanner
	return p.RelsWith(source, &s)
}

// RelsWith is Rels using the caller's scanner, which a caller that reads
// the relationships of many parts keeps, with its buffers.
func (p *Package) RelsWith(source string, s *xmlscan.Scanner) ([]Rel, error) {
	name := RelsName(source)
	if !p.Has(name) {
		return nil, nil
	}
	c, err := p.Scan(name, s, scanOptions)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var rels []Rel
	cut := int64(0) // external targets cut to their bound
	defer func() { p.cutTargets(name, cut) }()
	for {
		switch s.Next() {
		case xmlscan.Start:
			if !s.Is(ns.PackageRels, "Relationship") {
				continue
			}
			id, _ := s.Attr(xmlscan.NoSpace, "Id")
			typ, _ := s.Attr(xmlscan.NoSpace, "Type")
			target, _ := s.Attr(xmlscan.NoSpace, "Target")
			mode, _ := s.Attr(xmlscan.NoSpace, "TargetMode")
			r := Rel{ID: string(id), Type: string(typ), External: string(mode) == "External"}
			if r.External {
				if clean.AddressCut(string(target)) {
					cut++
				}
				r.Target = clean.Address(string(target))
			} else {
				r.Target = Resolve(source, string(target))
			}
			rels = append(rels, r)
		case xmlscan.EOF:
			if err := s.Err(); err != nil {
				if errors.Is(err, ErrBudget) {
					return rels, err
				}
				return rels, fmt.Errorf("%w: %s: %v", ErrDamaged, name, err)
			}
			return rels, nil
		}
	}
}

// Format is one of the document families.
type Format string

const (
	Docx Format = "docx"
	Xlsx Format = "xlsx"
	Pptx Format = "pptx"
	Vsdx Format = "vsdx"
)

// RelVisioDocument is the type of the relationship from a Visio package to
// its drawing, in place of the officeDocument relationship of the other
// formats.
const RelVisioDocument = "http://schemas.microsoft.com/visio/2010/relationships/document"

type kind struct {
	format Format
	mime   string
}

// mainKinds maps the content type of a main part to the family and to the
// media type of a file of that kind.
var mainKinds = map[string]kind{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml":   {Docx, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.template.main+xml":   {Docx, "application/vnd.openxmlformats-officedocument.wordprocessingml.template"},
	"application/vnd.ms-word.document.macroEnabled.main+xml":                             {Docx, "application/vnd.ms-word.document.macroEnabled.12"},
	"application/vnd.ms-word.template.macroEnabledTemplate.main+xml":                     {Docx, "application/vnd.ms-word.template.macroEnabled.12"},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml":         {Xlsx, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.template.main+xml":      {Xlsx, "application/vnd.openxmlformats-officedocument.spreadsheetml.template"},
	"application/vnd.ms-excel.sheet.macroEnabled.main+xml":                               {Xlsx, "application/vnd.ms-excel.sheet.macroEnabled.12"},
	"application/vnd.ms-excel.template.macroEnabled.main+xml":                            {Xlsx, "application/vnd.ms-excel.template.macroEnabled.12"},
	"application/vnd.ms-excel.sheet.binary.macroEnabled.main":                            {Xlsx, "application/vnd.ms-excel.sheet.binary.macroEnabled.12"},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml": {Pptx, "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	"application/vnd.openxmlformats-officedocument.presentationml.slideshow.main+xml":    {Pptx, "application/vnd.openxmlformats-officedocument.presentationml.slideshow"},
	"application/vnd.openxmlformats-officedocument.presentationml.template.main+xml":     {Pptx, "application/vnd.openxmlformats-officedocument.presentationml.template"},
	"application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml":                   {Pptx, "application/vnd.ms-powerpoint.presentation.macroEnabled.12"},
	"application/vnd.ms-powerpoint.slideshow.macroEnabled.main+xml":                      {Pptx, "application/vnd.ms-powerpoint.slideshow.macroEnabled.12"},
	"application/vnd.ms-powerpoint.template.macroEnabled.main+xml":                       {Pptx, "application/vnd.ms-powerpoint.template.macroEnabled.12"},
	"application/vnd.ms-visio.drawing.main+xml":                                          {Vsdx, "application/vnd.ms-visio.drawing"},
	"application/vnd.ms-visio.drawing.macroEnabled.main+xml":                             {Vsdx, "application/vnd.ms-visio.drawing.macroEnabled.12"},
	"application/vnd.ms-visio.template.main+xml":                                         {Vsdx, "application/vnd.ms-visio.template"},
	"application/vnd.ms-visio.template.macroEnabled.main+xml":                            {Vsdx, "application/vnd.ms-visio.template.macroEnabled.12"},
	"application/vnd.ms-visio.stencil.main+xml":                                          {Vsdx, "application/vnd.ms-visio.stencil"},
	"application/vnd.ms-visio.stencil.macroEnabled.main+xml":                             {Vsdx, "application/vnd.ms-visio.stencil.macroEnabled.12"},
}

// conventional are the usual locations of the main part, used when a
// package's content types or relationships do not identify it.
var conventional = []struct {
	name string
	kind
}{
	{"word/document.xml", mainKinds["application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"]},
	{"xl/workbook.xml", mainKinds["application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"]},
	{"xl/workbook.bin", mainKinds["application/vnd.ms-excel.sheet.binary.macroEnabled.main"]},
	{"ppt/presentation.xml", mainKinds["application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"]},
	{"visio/document.xml", mainKinds["application/vnd.ms-visio.drawing.main+xml"]},
}

// Main finds the main part of the package and says what kind of document
// it is.
//
// It follows the package's officeDocument relationship and reads the
// target's content type, which is how Office itself decides. A package
// whose relationship or content type is missing or wrong is still
// identified when a main part sits in its conventional place.
func (p *Package) Main() (part string, format Format, mime string, err error) {
	rels, relErr := p.Rels("")
	if errors.Is(relErr, ErrBudget) {
		return "", "", "", relErr
	}
	for _, r := range rels {
		if r.External || !(ns.IsRel(r.Type, ns.RelOfficeDocument) || r.Type == RelVisioDocument) || !p.Has(r.Target) {
			continue
		}
		if k, ok := mainKinds[p.ContentType(r.Target)]; ok {
			return r.Target, k.format, k.mime, nil
		}
		// The relationship is right and the content type is not one of
		// ours: go by where the part is.
		for _, c := range conventional {
			if r.Target == c.name {
				return r.Target, c.format, c.mime, nil
			}
		}
	}
	// No usable relationship. A declared main part still identifies the file.
	for _, name := range p.names {
		if k, ok := mainKinds[p.overrides[name]]; ok {
			return name, k.format, k.mime, nil
		}
	}
	for _, c := range conventional {
		if p.Has(c.name) {
			return c.name, c.format, c.mime, nil
		}
	}
	return "", "", "", ErrNoMainPart
}
