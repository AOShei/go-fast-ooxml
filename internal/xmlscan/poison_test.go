//go:build xmlscanpoison

package xmlscan

import (
	"bytes"
	"strings"
	"testing"
)

// TestPoisonOverwritesHeldViews checks the xmlscanpoison tag does what CI
// relies on it for: a view of a token kept past the next call to Next
// reads '#', whether it was a view of the window or of a decoded buffer.
func TestPoisonOverwritesHeldViews(t *testing.T) {
	const doc = `<a x="plain" y="a&amp;b"><b>text</b>more &amp; more</a>`
	s := New(strings.NewReader(doc), &Options{BufferSize: 4096})
	s.Next() // <a>
	x, _ := s.Attr(NoSpace, "x")
	y, _ := s.Attr(NoSpace, "y")
	s.Next() // <b>
	name := s.Local()
	s.Next() // text
	text := s.Text()
	s.Next() // </b>
	end := s.Local()
	s.Next() // more & more
	decoded := s.Text()
	s.Next() // </a>
	for _, v := range []struct {
		what string
		b    []byte
	}{{"attribute", x}, {"decoded attribute", y}, {"name", name}, {"text", text}, {"end name", end}, {"decoded text", decoded}} {
		if len(v.b) == 0 || len(bytes.Trim(v.b, "#")) != 0 {
			t.Errorf("%s held past Next reads %q, want only '#'", v.what, v.b)
		}
	}
}
