package xmlscan

import (
	"io"
	"testing"
	"testing/iotest"
)

// Text in the code pages of Windows, and in ISO 8859-15, is read as the
// characters it stands for, also when a double-byte character is split
// between two reads.
func TestCodePages(t *testing.T) {
	for _, c := range []struct {
		enc  string
		text []byte
		want string
	}{
		{"windows-1251", []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}, "Привет"},
		{"windows-1250", []byte{0x8A, 0xE8}, "Šč"},
		{"ISO-8859-15", []byte{0xA4, 0xBD}, "€œ"},
		{"Shift_JIS", []byte{0x93, 0xFA, 0x96, 0x7B}, "日本"},
		{"GBK", []byte{0xD6, 0xD0, 0xCE, 0xC4}, "中文"},
	} {
		doc := append([]byte(`<?xml version="1.0" encoding="`+c.enc+`"?><a>`), c.text...)
		doc = append(doc, "</a>"...)
		for name, r := range map[string]io.Reader{"whole": &onceReader{doc}, "a byte at a time": iotest.OneByteReader(&onceReader{doc})} {
			s := New(r, nil)
			got := ""
			for s.Next() != EOF {
				if s.Kind() == Text {
					got += string(s.Text())
				}
			}
			if got != c.want || s.Err() != nil {
				t.Errorf("%s, %s: %q %v, want %q", c.enc, name, got, s.Err(), c.want)
			}
		}
	}
}

type onceReader struct{ b []byte }

func (o *onceReader) Read(p []byte) (int, error) {
	if len(o.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, o.b)
	o.b = o.b[n:]
	return n, nil
}
