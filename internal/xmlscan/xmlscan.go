// Package xmlscan is a pull tokenizer for XML, built for reading Office
// Open XML parts quickly and safely.
//
// It differs from encoding/xml in what it is for:
//
//   - It does not allocate per token. Names, attribute values and text are
//     slices of the scanner's window, valid until the next call to Next.
//   - It reads through a window that is refilled as it goes, so a part of
//     any size is read in bounded memory. Text is delivered in chunks when
//     it does not fit; a tag must fit, up to Options.MaxTokenBytes.
//   - It resolves namespace prefixes to small numbers the caller chooses,
//     so "is this w:p?" is an integer and a short byte comparison.
//   - It refuses document type declarations, which removes entity-expansion
//     attacks, and bounds nesting depth.
//   - Attributes are found on demand, not built into a list.
//
// It checks what it needs to stay correct (tags match, markup is
// terminated) and is lenient about the rest: an unknown entity reference is
// passed through as written.
package xmlscan

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// Kind is the type of a token.
type Kind uint8

const (
	// EOF means the input ended or an error occurred. Err tells which.
	EOF Kind = iota
	// Start is a start tag. An empty-element tag (<a/>) gives Start and
	// then End.
	Start
	// End is an end tag.
	End
	// Text is character data or a CDATA section. One run of text in the
	// source may arrive as several Text tokens.
	Text
)

// Space identifies an XML namespace. Callers number the namespaces they
// care about from FirstSpace and pass the numbering in Options.Spaces.
type Space uint16

const (
	// NoSpace is the absence of a namespace: an attribute without a
	// prefix, or an element where no default namespace is in force.
	NoSpace Space = iota
	// SpaceXML is the namespace of the reserved "xml" prefix (xml:space).
	SpaceXML
	// SpaceUnknown is any namespace not listed in Options.Spaces, and any
	// prefix that was never declared.
	SpaceUnknown
	// FirstSpace is the first number available to callers.
	FirstSpace
)

// Errors reported by Err. Syntax errors wrap ErrSyntax and carry the byte
// offset.
var (
	ErrSyntax        = errors.New("xmlscan: malformed XML")
	ErrDTD           = errors.New("xmlscan: document type declarations are not accepted")
	ErrDepth         = errors.New("xmlscan: elements nested too deeply")
	ErrTokenTooLarge = errors.New("xmlscan: a tag is larger than the token limit")
	ErrEncoding      = errors.New("xmlscan: unsupported character encoding")
)

// Options configures a Scanner. The zero value is usable.
type Options struct {
	// Spaces maps namespace URIs to the caller's numbers. It is only read.
	Spaces map[string]Space
	// MaxDepth bounds element nesting. Zero means DefaultMaxDepth.
	MaxDepth int
	// MaxTokenBytes bounds the size of one tag, and so of one attribute
	// value. Zero means DefaultMaxTokenBytes.
	MaxTokenBytes int
	// BufferSize is the initial window size when reading from an
	// io.Reader. Zero means DefaultBufferSize.
	BufferSize int
	// Recover makes the scanner carry on when end tags do not match start
	// tags, the way a browser reads HTML: an end tag that matches an
	// element further out closes everything inside it, and one that
	// matches nothing is dropped. Each such fix is counted in Repairs.
	// It is for parts known to be written loosely, such as legacy VML
	// drawings, which contain fragments like "<br>" with no end tag.
	Recover bool
}

// Defaults for Options.
const (
	DefaultMaxDepth      = 512
	DefaultMaxTokenBytes = 16 << 20
	DefaultBufferSize    = 64 << 10
)

type attr struct {
	name, value []byte
}

type elem struct {
	nameStart int32 // offset of the qualified name in Scanner.names
	nameLen   int32
	colon     int32 // index of ':' in the name, or -1
	space     Space
	bindMark  int32 // len(binds) before this element's declarations
	bindBytes int32 // len(bindNames) before this element's declarations
}

type binding struct {
	start, end int32 // prefix in Scanner.bindNames; empty for the default namespace
	space      Space
	slot       int32 // index in Scanner.heads of this prefix
	prev       int32 // the binding this one shadows, or -1
}

// Above this many namespace declarations in scope, prefixes are looked up
// in a hash table. Below it a scan from the innermost is faster. The table
// is what keeps a file that declares thousands of prefixes from making
// every element expensive.
const linearBindings = 8

// maxRepairs bounds how much mending Options.Recover will do. Each repair
// may search the whole element stack, so without a bound a part made of
// nothing but mismatched tags would be slow to read.
const maxRepairs = 10000

type mode uint8

const (
	modeNormal mode = iota
	modeComment
	modeCDATA
)

// Scanner reads XML tokens. Create one with New or NewBytes, or reuse one
// with Reset or ResetBytes, which keeps its buffers.
type Scanner struct {
	r    io.Reader
	buf  []byte // the window
	pos  int    // start of the unconsumed input in buf
	end  int    // end of valid data in buf
	base int64  // offset in the input of buf[0]
	eof  bool   // the reader is exhausted
	own  bool   // buf belongs to the scanner and may be written
	err  error
	late error // an error the reader gave with its last bytes: met once they are scanned
	mode mode

	spaces   map[string]Space
	maxDepth int
	maxToken int
	recover  bool
	closing  int // elements still to be closed by a recovered end tag

	// The current token.
	kind       Kind
	qname      []byte
	local      []byte
	space      Space
	text       []byte
	attrs      []attr
	pendingEnd bool // an empty-element tag still owes its End

	stack     []elem
	names     []byte // qualified names of the open elements
	binds     []binding
	bindNames []byte
	slots     map[string]int32 // prefix -> index in heads; kept across resets
	heads     []int32          // per prefix: its innermost binding, or -1
	scratch   []byte           // decoded text of the current token
	attrBuf   []byte           // decoded attribute values of the current token

	// Repairs counts the places where malformed input was read leniently:
	// an unknown or unterminated entity reference kept as written and,
	// with Options.Recover, a mismatched end tag.
	Repairs int
}

// New returns a Scanner reading from r.
func New(r io.Reader, opts *Options) *Scanner {
	s := &Scanner{}
	s.Reset(r, opts)
	return s
}

// NewBytes returns a Scanner reading from data, which it does not modify
// or copy.
func NewBytes(data []byte, opts *Options) *Scanner {
	s := &Scanner{}
	s.ResetBytes(data, opts)
	return s
}

// Reset makes the scanner read from r, keeping its buffers.
func (s *Scanner) Reset(r io.Reader, opts *Options) {
	size := DefaultBufferSize
	if opts != nil && opts.BufferSize > 0 {
		size = opts.BufferSize
	}
	if !s.own || len(s.buf) < size {
		s.buf = make([]byte, size)
	}
	s.reset(opts)
	s.r, s.own = r, true
	s.sniff()
}

// ResetBytes makes the scanner read from data, keeping its buffers.
func (s *Scanner) ResetBytes(data []byte, opts *Options) {
	s.reset(opts)
	s.r, s.own = nil, false
	s.buf, s.end, s.eof = data, len(data), true
	s.sniff()
}

func (s *Scanner) reset(opts *Options) {
	s.pos, s.end, s.base, s.eof, s.err, s.late, s.mode = 0, 0, 0, false, nil, nil, modeNormal
	s.kind, s.pendingEnd, s.Repairs = EOF, false, 0
	s.stack, s.names, s.binds, s.bindNames = s.stack[:0], s.names[:0], s.binds[:0], s.bindNames[:0]
	if len(s.slots) > 1024 {
		s.slots, s.heads = nil, s.heads[:0] // do not carry a hostile file's prefixes around
	}
	for i := range s.heads {
		s.heads[i] = -1
	}
	s.attrs, s.scratch, s.attrBuf = s.attrs[:0], s.scratch[:0], s.attrBuf[:0]
	s.spaces, s.maxDepth, s.maxToken, s.recover, s.closing = nil, DefaultMaxDepth, DefaultMaxTokenBytes, false, 0
	if opts != nil {
		s.spaces, s.recover = opts.Spaces, opts.Recover
		if opts.MaxDepth > 0 {
			s.maxDepth = opts.MaxDepth
		}
		if opts.MaxTokenBytes > 0 {
			s.maxToken = opts.MaxTokenBytes
		}
	}
}

// Err returns the error that ended the scan, or nil at a clean end of
// input.
func (s *Scanner) Err() error { return s.err }

// Offset returns the byte offset in the (decoded) input of the next
// unread byte.
func (s *Scanner) Offset() int64 { return s.base + int64(s.pos) }

// Depth returns the number of open elements. After a Start token it
// includes that element; after an End token it does not.
func (s *Scanner) Depth() int { return len(s.stack) }

// Kind returns the kind of the current token, as last returned by Next.
func (s *Scanner) Kind() Kind { return s.kind }

// Local returns the local name of the current Start or End token.
func (s *Scanner) Local() []byte { return s.local }

// Space returns the namespace of the current Start or End token.
func (s *Scanner) Space() Space { return s.space }

// Name returns the qualified name of the current Start or End token as it
// is written in the source, prefix included.
func (s *Scanner) Name() []byte { return s.qname }

// Is reports whether the current Start or End token is the element with
// the given namespace and local name.
func (s *Scanner) Is(space Space, local string) bool {
	return s.space == space && string(s.local) == local
}

// Text returns the content of the current Text token, with entity and
// character references decoded and line ends normalized to "\n".
func (s *Scanner) Text() []byte { return s.text }

func (s *Scanner) fail(err error) {
	if s.err == nil {
		s.err = err
	}
	s.kind = EOF
}

func (s *Scanner) syntax(what string) {
	if s.late != nil {
		// The input stopped because the reader failed, not because the
		// XML is wrong: the reader's error is the one to give.
		s.fail(s.late)
		return
	}
	s.fail(fmt.Errorf("%w at byte %d: %s", ErrSyntax, s.Offset(), what))
}

// more reads more input into the window, keeping the unconsumed part,
// which moves to the front. It reports whether any bytes were added.
func (s *Scanner) more() bool {
	if s.eof && s.late != nil && s.err == nil {
		// The bytes that came with the error have been scanned.
		s.fail(s.late)
	}
	if s.eof || s.err != nil {
		return false
	}
	if s.pos > 0 {
		n := copy(s.buf, s.buf[s.pos:s.end])
		s.base += int64(s.pos)
		s.pos, s.end = 0, n
	}
	if s.end == len(s.buf) {
		if len(s.buf) >= s.maxToken {
			s.fail(fmt.Errorf("%w (%d bytes) at byte %d", ErrTokenTooLarge, s.maxToken, s.Offset()))
			return false
		}
		grown := make([]byte, min(2*len(s.buf), s.maxToken))
		copy(grown, s.buf[:s.end])
		s.buf = grown
	}
	for range 64 {
		n, err := s.r.Read(s.buf[s.end:])
		s.end += n
		if err != nil {
			s.eof = true
			if err != io.EOF {
				// The bytes that came with the error are scanned first,
				// so that where the input stops does not depend on how
				// the reader divides it.
				if n > 0 {
					s.late = err
					return true
				}
				s.fail(err)
				return false
			}
			return n > 0
		}
		if n > 0 {
			return true
		}
	}
	s.fail(io.ErrNoProgress)
	return false
}

// Abort ends the scan: from now on Next returns EOF and Err returns err,
// unless the scan had failed already. It is for a reader that has reason
// of its own to stop, such as a limit on what it may produce.
func (s *Scanner) Abort(err error) { s.fail(err) }

// Next advances to the next token and returns its kind.
func (s *Scanner) Next() Kind {
	if s.pendingEnd {
		s.pendingEnd = false
		s.pop()
		s.kind = End
		return End
	}
	if s.err != nil {
		return EOF
	}
	if s.closing > 0 {
		s.closing--
		s.pop()
		s.kind = End
		return End
	}
	s.attrs, s.attrBuf = s.attrs[:0], s.attrBuf[:0]
	for {
		if s.pos >= s.end && !s.more() {
			if s.err == nil && len(s.stack) > 0 && s.recover && s.mode == modeNormal {
				// Recovering: close what the input left open.
				s.Repairs++
				s.closing = len(s.stack) - 1
				s.pop()
				s.kind = End
				return End
			}
			if s.err == nil && (len(s.stack) > 0 || s.mode != modeNormal) {
				s.syntax("unexpected end of input")
			}
			s.kind = EOF
			return EOF
		}
		var status scanStatus
		switch {
		case s.mode == modeComment:
			status = s.comment()
		case s.mode == modeCDATA:
			status = s.cdata()
		case s.buf[s.pos] != '<':
			status = s.charData()
		default:
			status = s.markup()
		}
		switch status {
		case emitted:
			return s.kind
		case needMore:
			atEnd := s.eof
			if !s.more() {
				if s.err != nil {
					return EOF
				}
				if atEnd {
					s.syntax("unexpected end of input")
					return EOF
				}
				// The end of the input has just been found. Go round
				// again: text that runs to the end is complete, markup
				// that does is not, and each construct knows which.
			}
		case failed:
			return EOF
		}
		// skipped: go round again
	}
}

type scanStatus uint8

const (
	emitted  scanStatus = iota // a token is ready
	skipped                    // input was consumed without producing a token
	needMore                   // the window ends inside the construct
	failed
)

// charData scans text up to the next '<'.
func (s *Scanner) charData() scanStatus {
	b := s.buf[s.pos:s.end]
	n := bytes.IndexByte(b, '<')
	if n < 0 {
		switch {
		case s.eof:
			n = len(b)
		case s.pos > 0 || s.end < len(s.buf):
			return needMore // there is room to look further before splitting
		default:
			// The window is full of text. Hand over what can be cut
			// cleanly and carry on; text need not fit in the window.
			if n = safeCut(b); n == 0 {
				return needMore
			}
		}
	}
	raw := b[:n]
	s.pos += n
	s.text = s.decodeText(raw)
	s.kind = Text
	return emitted
}

// safeCut returns how much of b can be delivered as a text chunk without
// splitting a UTF-8 sequence, an entity reference or a CR LF pair.
func safeCut(b []byte) int {
	n := len(b)
	if n > 0 && b[n-1] == '\r' {
		n--
	}
	// An incomplete multi-byte sequence at the end.
	for back := 1; back <= 3 && back <= n; back++ {
		c := b[n-back]
		if c < 0x80 {
			break
		}
		if c >= 0xC0 { // a lead byte: is its sequence complete?
			need := 2
			if c >= 0xF0 {
				need = 4
			} else if c >= 0xE0 {
				need = 3
			}
			if back < need {
				n -= back
			}
			break
		}
	}
	// An entity reference that has started but not ended.
	from := max(0, n-16)
	if i := bytes.LastIndexByte(b[from:n], '&'); i >= 0 {
		if bytes.IndexByte(b[from+i:n], ';') < 0 {
			n = from + i
		}
		return n
	}
	// And further back, a character written by its number with many
	// zeros in front: "&#", then digits to the end.
	if i := bytes.LastIndexByte(b[:n], '&'); i >= 0 && i+1 < n && b[i+1] == '#' {
		for _, c := range b[i+2 : n] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == 'x' || c == 'X') {
				return n
			}
		}
		n = i
	}
	return n
}

// comment skips to the end of a comment, which may be larger than the
// window.
func (s *Scanner) comment() scanStatus {
	b := s.buf[s.pos:s.end]
	if i := bytes.Index(b, []byte("-->")); i >= 0 {
		s.pos += i + 3
		s.mode = modeNormal
		return skipped
	}
	if s.eof {
		s.syntax("comment is not terminated")
		return failed
	}
	// Keep the last two bytes: they may be the start of the terminator.
	if len(b) > 2 {
		s.pos = s.end - 2
	}
	return needMore
}

// cdata delivers the content of a CDATA section as text, in chunks if it
// is larger than the window.
func (s *Scanner) cdata() scanStatus {
	b := s.buf[s.pos:s.end]
	if i := bytes.Index(b, []byte("]]>")); i >= 0 {
		s.text = s.normalizeLines(b[:i])
		s.pos += i + 3
		s.mode = modeNormal
		if i == 0 {
			return skipped
		}
		s.kind = Text
		return emitted
	}
	if s.eof {
		s.syntax("CDATA section is not terminated")
		return failed
	}
	if s.pos > 0 || s.end < len(s.buf) {
		return needMore
	}
	// A full window of CDATA: deliver all but a possible partial terminator.
	n := len(b) - 2
	if n > 0 && b[n-1] == '\r' {
		n--
	}
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	if n <= 0 {
		return needMore
	}
	s.text = s.normalizeLines(b[:n])
	s.pos += n
	s.kind = Text
	return emitted
}

var (
	cdataOpen   = []byte("<![CDATA[")
	commentOpen = []byte("<!--")
	doctypeOpen = []byte("<!DOCTYPE")
)

// markup scans a construct that starts with '<'.
func (s *Scanner) markup() scanStatus {
	b := s.buf[s.pos:s.end]
	if len(b) < 2 {
		return needMore
	}
	switch b[1] {
	case '/':
		// The usual case: the end tag of the innermost element, written
		// with no space. Compare it in place.
		if n := len(s.stack); n > 0 {
			top := &s.stack[n-1]
			end := 2 + int(top.nameLen)
			if end < len(b) && b[end] == '>' && string(b[2:end]) == string(s.names[top.nameStart:top.nameStart+top.nameLen]) {
				s.pos += end + 1
				s.pop()
				s.kind = End
				return emitted
			}
		}
		i := bytes.IndexByte(b, '>')
		if i < 0 {
			return needMore
		}
		name := b[2:i]
		for len(name) > 0 && isSpace(name[len(name)-1]) {
			name = name[:len(name)-1]
		}
		matched := len(s.stack) - 1
		if matched >= 0 && !s.named(matched, name) {
			if !s.recover {
				top := &s.stack[matched]
				s.syntax(fmt.Sprintf("end tag </%s> does not match <%s>", clip(name), clip(s.names[top.nameStart:top.nameStart+top.nameLen])))
				return failed
			}
			for matched--; matched >= 0 && !s.named(matched, name); matched-- {
			}
		}
		if matched < 0 {
			if !s.recover {
				s.syntax("end tag without a start tag")
				return failed
			}
			// Recovering: an end tag that closes nothing is dropped.
			if s.repaired() {
				return failed
			}
			s.pos += i + 1
			return skipped
		}
		// Recovering closes the elements left open inside the matched one
		// first; closing is then non-zero and Next hands out their Ends.
		s.closing = len(s.stack) - 1 - matched
		if s.closing > 0 && s.repaired() {
			return failed
		}
		s.pos += i + 1
		s.pop()
		s.kind = End
		return emitted

	case '?':
		i := bytes.Index(b, []byte("?>"))
		if i < 0 {
			return needMore
		}
		s.pos += i + 2
		return skipped

	case '!':
		switch {
		case bytes.HasPrefix(b, commentOpen):
			s.pos += len(commentOpen)
			s.mode = modeComment
			return skipped
		case bytes.HasPrefix(b, cdataOpen):
			s.pos += len(cdataOpen)
			s.mode = modeCDATA
			return skipped
		case len(b) >= len(doctypeOpen) && bytes.EqualFold(b[:len(doctypeOpen)], doctypeOpen):
			s.fail(fmt.Errorf("%w (at byte %d)", ErrDTD, s.Offset()))
			return failed
		case len(b) < len(cdataOpen) && !s.eof:
			return needMore
		}
		s.syntax("unrecognized markup after <!")
		return failed
	}
	return s.startTag(b)
}

// repaired counts one repair made by Options.Recover and reports whether
// that was one too many, in which case the scan has failed.
func (s *Scanner) repaired() bool {
	s.Repairs++
	if s.Repairs > maxRepairs {
		s.closing = 0
		s.syntax("too many mismatched tags to recover from")
		return true
	}
	return false
}

// named reports whether the open element at stack index i has the given
// qualified name.
func (s *Scanner) named(i int, name []byte) bool {
	e := &s.stack[i]
	return string(s.names[e.nameStart:e.nameStart+e.nameLen]) == string(name)
}

func isSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\t' || c == '\r' }

func clip(b []byte) []byte {
	if len(b) > 40 {
		return append(append([]byte{}, b[:40]...), "..."...)
	}
	return b
}

// startTag parses a start tag or empty-element tag beginning at b[0]. The
// whole tag must be in the window.
func (s *Scanner) startTag(b []byte) scanStatus {
	i, colon := 1, -1
	for ; i < len(b); i++ {
		c := b[i]
		if c == '>' || c == '/' || c == ' ' || c == '\n' || c == '\t' || c == '\r' {
			break
		}
		if c == ':' && colon < 0 {
			colon = i - 1
		}
	}
	if i == len(b) {
		return needMore
	}
	name := b[1:i]
	if len(name) == 0 {
		s.syntax("tag without a name")
		return failed
	}
	s.attrs = s.attrs[:0]
	selfClose, declares := false, false
	for {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return needMore
		}
		if b[i] == '>' {
			i++
			break
		}
		if b[i] == '/' {
			if i+1 == len(b) {
				return needMore
			}
			if b[i+1] != '>' {
				s.syntax("stray / in a tag")
				return failed
			}
			selfClose = true
			i += 2
			break
		}
		// An attribute: name, '=', quoted value.
		start := i
		for i < len(b) && b[i] != '=' && !isSpace(b[i]) && b[i] != '>' && b[i] != '/' {
			i++
		}
		if i == len(b) {
			return needMore
		}
		attrName := b[start:i]
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return needMore
		}
		if b[i] != '=' || len(attrName) == 0 {
			s.syntax("attribute without a value")
			return failed
		}
		i++
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) {
			return needMore
		}
		quote := b[i]
		if quote != '"' && quote != '\'' {
			s.syntax("attribute value is not quoted")
			return failed
		}
		i++
		// Most values are a few bytes long: look by hand first, and only
		// call out for the long ones.
		n, limit := 0, min(len(b)-i, 24)
		for n < limit && b[i+n] != quote {
			n++
		}
		if n == limit {
			m := bytes.IndexByte(b[i+n:], quote)
			if m < 0 {
				return needMore
			}
			n += m
		}
		if attrName[0] == 'x' && len(attrName) >= 5 {
			declares = true
		}
		s.attrs = append(s.attrs, attr{attrName, b[i : i+n]})
		i += n + 1
	}

	if len(s.stack) >= s.maxDepth {
		s.fail(fmt.Errorf("%w (limit %d) at byte %d", ErrDepth, s.maxDepth, s.Offset()))
		return failed
	}
	s.push(name, colon, declares)
	s.pos += i
	s.pendingEnd = selfClose
	s.kind = Start
	return emitted
}

var xmlnsPrefix = []byte("xmlns")

// push opens an element: it records the namespace declarations among the
// attributes just parsed, resolves the element's own namespace and puts
// the element on the stack. colon is the index of ':' in name, or -1.
// declares says an attribute name starts with 'x', the only way the tag
// can hold a namespace declaration.
func (s *Scanner) push(name []byte, colon int, declares bool) {
	e := elem{
		nameStart: int32(len(s.names)),
		nameLen:   int32(len(name)),
		colon:     int32(colon),
		bindMark:  int32(len(s.binds)),
		bindBytes: int32(len(s.bindNames)),
	}
	for i := 0; declares && i < len(s.attrs); i++ {
		a := &s.attrs[i]
		if len(a.name) < 5 || a.name[0] != 'x' || !bytes.HasPrefix(a.name, xmlnsPrefix) {
			continue
		}
		var prefix []byte
		switch {
		case len(a.name) == 5:
		case a.name[5] == ':':
			prefix = a.name[6:]
		default:
			continue // an ordinary attribute whose name starts with "xmlns"
		}
		uri := a.value
		if bytes.IndexByte(uri, '&') >= 0 {
			s.attrBuf, uri = s.decodeAttr(s.attrBuf, uri)
		}
		space := SpaceUnknown
		if len(uri) == 0 {
			space = NoSpace // xmlns="" undeclares the default namespace
		} else if v, ok := s.spaces[string(uri)]; ok {
			space = v
		}
		slot, known := s.slots[string(prefix)]
		if !known {
			if s.slots == nil {
				s.slots = map[string]int32{}
			}
			slot = int32(len(s.heads))
			s.slots[string(prefix)] = slot
			s.heads = append(s.heads, -1)
		}
		start := int32(len(s.bindNames))
		s.bindNames = append(s.bindNames, prefix...)
		s.binds = append(s.binds, binding{start, int32(len(s.bindNames)), space, slot, s.heads[slot]})
		s.heads[slot] = int32(len(s.binds) - 1)
	}

	var prefix []byte
	if e.colon >= 0 {
		prefix = name[:e.colon]
	}
	// A child usually has its parent's prefix. If this tag declared
	// nothing, the parent's answer is the answer.
	if n := len(s.stack); n > 0 && int(e.bindMark) == len(s.binds) {
		p := &s.stack[n-1]
		var parentPrefix []byte
		if p.colon >= 0 {
			parentPrefix = s.names[p.nameStart : p.nameStart+p.colon]
		}
		if string(parentPrefix) == string(prefix) {
			e.space = p.space
			goto resolved
		}
	}
	e.space = s.resolve(prefix, true)
resolved:
	s.names = append(s.names, name...)
	s.stack = append(s.stack, e)
	s.qname = s.names[e.nameStart : e.nameStart+e.nameLen]
	s.local = s.qname[e.colon+1:]
	s.space = e.space
}

// resolve returns the namespace bound to prefix. An element without a
// prefix takes the default namespace; an attribute without one has none.
func (s *Scanner) resolve(prefix []byte, element bool) Space {
	if len(prefix) == 0 && !element {
		return NoSpace
	}
	if len(s.binds) <= linearBindings {
		for i := len(s.binds) - 1; i >= 0; i-- {
			b := &s.binds[i]
			if string(s.bindNames[b.start:b.end]) == string(prefix) {
				return b.space
			}
		}
	} else if slot, ok := s.slots[string(prefix)]; ok && s.heads[slot] >= 0 {
		return s.binds[s.heads[slot]].space
	}
	switch {
	case len(prefix) == 0:
		return NoSpace
	case string(prefix) == "xml":
		return SpaceXML
	}
	return SpaceUnknown
}

// pop closes the innermost element and makes it the current token.
func (s *Scanner) pop() {
	e := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	s.qname = s.names[e.nameStart : e.nameStart+e.nameLen]
	s.local = s.qname[e.colon+1:]
	s.space = e.space
	s.names = s.names[:e.nameStart]
	for i := len(s.binds) - 1; i >= int(e.bindMark); i-- {
		s.heads[s.binds[i].slot] = s.binds[i].prev // what this element shadowed is visible again
	}
	s.binds = s.binds[:e.bindMark]
	s.bindNames = s.bindNames[:e.bindBytes]
}

// Attr returns the value of the attribute with the given namespace and
// local name on the current Start token. An attribute written without a
// prefix has namespace NoSpace. The value has references decoded and is
// valid until the next call to Next.
func (s *Scanner) Attr(space Space, local string) ([]byte, bool) {
	for i := range s.attrs {
		a := &s.attrs[i]
		colon := bytes.IndexByte(a.name, ':')
		if string(a.name[colon+1:]) != local {
			continue
		}
		var prefix []byte
		if colon >= 0 {
			prefix = a.name[:colon]
		}
		if s.resolve(prefix, false) != space {
			continue
		}
		return s.attrValue(a), true
	}
	return nil, false
}

// AttrAny returns the value of the first attribute with the given local
// name, whatever its namespace. It is for attributes that producers write
// both with and without a prefix.
func (s *Scanner) AttrAny(local string) ([]byte, bool) {
	for i := range s.attrs {
		a := &s.attrs[i]
		if string(a.name[bytes.IndexByte(a.name, ':')+1:]) == local {
			return s.attrValue(a), true
		}
	}
	return nil, false
}

// NumAttr returns the number of attributes on the current Start token,
// namespace declarations included.
func (s *Scanner) NumAttr() int { return len(s.attrs) }

// AttrAt returns the i-th attribute of the current Start token: its
// namespace, local name and decoded value.
func (s *Scanner) AttrAt(i int) (space Space, local, value []byte) {
	a := &s.attrs[i]
	colon := bytes.IndexByte(a.name, ':')
	var prefix []byte
	if colon >= 0 {
		prefix = a.name[:colon]
	}
	return s.resolve(prefix, false), a.name[colon+1:], s.attrValue(a)
}

// AttrNameAt returns the qualified name of the i-th attribute of the
// current Start token as written in the source, prefix included.
func (s *Scanner) AttrNameAt(i int) []byte { return s.attrs[i].name }

func (s *Scanner) attrValue(a *attr) []byte {
	if !needsAttrDecode(a.value) {
		return a.value
	}
	var v []byte
	s.attrBuf, v = s.decodeAttr(s.attrBuf, a.value)
	return v
}

func needsAttrDecode(v []byte) bool {
	for _, c := range v {
		if c == '&' || c == '\t' || c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

// decodeAttr appends the decoded form of raw to buf and returns the
// extended buffer and the decoded value within it. White space characters
// written literally become spaces, as XML requires for attribute values.
func (s *Scanner) decodeAttr(buf, raw []byte) (grown, value []byte) {
	start := len(buf)
	for i := 0; i < len(raw); {
		switch c := raw[i]; c {
		case '&':
			var n int
			buf, n = s.reference(buf, raw[i:])
			i += n
		case '\r':
			// Line ends are normalized first, so CR LF is one line end
			// and becomes one space.
			buf = append(buf, ' ')
			i++
			if i < len(raw) && raw[i] == '\n' {
				i++
			}
		case '\t', '\n':
			buf = append(buf, ' ')
			i++
		default:
			buf = append(buf, c)
			i++
		}
	}
	// A value that needed decoding may leave buf's backing array when it
	// grows; earlier values keep pointing at the old array, which stays
	// valid, so they are unaffected.
	return buf, buf[start:]
}

// decodeText returns raw with references decoded and line ends
// normalized. Text that needs neither is returned as it is.
func (s *Scanner) decodeText(raw []byte) []byte {
	if len(raw) <= 32 {
		// Short text, the usual case in a spreadsheet: one look by hand
		// is cheaper than two calls.
		plain := true
		for _, c := range raw {
			if c == '&' || c == '\r' {
				plain = false
				break
			}
		}
		if plain {
			return raw
		}
	}
	if bytes.IndexByte(raw, '&') < 0 {
		return s.normalizeLines(raw)
	}
	buf := s.scratch[:0]
	for i := 0; i < len(raw); {
		switch c := raw[i]; c {
		case '&':
			var n int
			buf, n = s.reference(buf, raw[i:])
			i += n
		case '\r':
			buf = append(buf, '\n')
			i++
			if i < len(raw) && raw[i] == '\n' {
				i++
			}
		default:
			// Copy the run up to the next special byte in one go.
			j := i + 1
			for j < len(raw) && raw[j] != '&' && raw[j] != '\r' {
				j++
			}
			buf = append(buf, raw[i:j]...)
			i = j
		}
	}
	s.scratch = buf
	return buf
}

// normalizeLines turns CR LF and lone CR into LF, as XML requires.
func (s *Scanner) normalizeLines(raw []byte) []byte {
	if bytes.IndexByte(raw, '\r') < 0 {
		return raw
	}
	buf := s.scratch[:0]
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\r' {
			c = '\n'
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
		}
		buf = append(buf, c)
	}
	s.scratch = buf
	return buf
}

// reference decodes the entity or character reference at the start of raw
// (raw[0] is '&'), appends the result to buf and returns how many bytes of
// raw it consumed. A reference that cannot be decoded is kept as written.
func (s *Scanner) reference(buf, raw []byte) ([]byte, int) {
	if len(raw) >= 2 && raw[1] == '#' {
		// A character by its number, which may be written with any
		// number of zeros in front: the digits run to the semicolon.
		i, base := 2, rune(10)
		if i < len(raw) && (raw[i] == 'x' || raw[i] == 'X') {
			base, i = 16, i+1
		}
		first := i
		var r rune
	digits:
		for ; i < len(raw); i++ {
			var d rune
			switch c := raw[i]; {
			case c >= '0' && c <= '9':
				d = rune(c - '0')
			case base == 16 && c >= 'a' && c <= 'f':
				d = rune(c-'a') + 10
			case base == 16 && c >= 'A' && c <= 'F':
				d = rune(c-'A') + 10
			default:
				break digits
			}
			if r = r*base + d; r > utf8.MaxRune {
				r = utf8.MaxRune + 1
			}
		}
		if i == first || i == len(raw) || raw[i] != ';' {
			s.Repairs++
			return append(buf, '&'), 1
		}
		if r == 0 || r > utf8.MaxRune || (r >= 0xD800 && r <= 0xDFFF) {
			r = utf8.RuneError
		}
		return utf8.AppendRune(buf, r), i + 1
	}
	end := bytes.IndexByte(raw[:min(len(raw), 12)], ';')
	if end < 2 {
		s.Repairs++
		return append(buf, '&'), 1
	}
	name := raw[1:end]
	switch string(name) {
	case "lt":
		return append(buf, '<'), end + 1
	case "gt":
		return append(buf, '>'), end + 1
	case "amp":
		return append(buf, '&'), end + 1
	case "quot":
		return append(buf, '"'), end + 1
	case "apos":
		return append(buf, '\''), end + 1
	}
	s.Repairs++
	return append(buf, '&'), 1
}

// Skip reads past the end of the element whose Start token is current. It
// reports false if the input ended first.
func (s *Scanner) Skip() bool {
	depth := len(s.stack)
	for len(s.stack) >= depth {
		if s.Next() == EOF {
			return false
		}
	}
	return true
}

// AppendText reads to the end of the element whose Start token is current
// and appends all character data inside it, at any depth, to dst.
func (s *Scanner) AppendText(dst []byte) []byte {
	depth := len(s.stack)
	for len(s.stack) >= depth {
		switch s.Next() {
		case Text:
			dst = append(dst, s.text...)
		case EOF:
			return dst
		}
	}
	return dst
}
