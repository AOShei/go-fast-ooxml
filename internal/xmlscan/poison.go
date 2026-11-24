//go:build xmlscanpoison

package xmlscan

// With the xmlscanpoison tag, the bytes of every token are overwritten
// with '#' once the next is asked for: in the window, and in the buffers
// that held its decoded text, attribute values and, once it has ended,
// its name. A reader that keeps a view of a token after the next call to
// Next then reads '#', where it would otherwise read whatever the window
// held by then, which depends on how the input arrived. Released windows
// are overwritten too. The tests are run with it in CI.
const poison = true

// Poisoned reports whether the xmlscanpoison tag is set, for tests that
// count allocations or time, which it changes.
const Poisoned = poison

func (s *Scanner) poisonViews() {
	if s.own {
		if s.poisoned > s.pos {
			s.poisoned = 0 // the window has moved
		}
		fill(s.buf[s.poisoned:s.pos])
		s.poisoned = s.pos
	}
	fill(s.scratch)
	fill(s.attrBuf)
	fill(s.names[len(s.names):cap(s.names)])
}

func (s *Scanner) poisonReleased() { fill(s.buf) }

func fill(b []byte) {
	for i := range b {
		b[i] = '#'
	}
}
