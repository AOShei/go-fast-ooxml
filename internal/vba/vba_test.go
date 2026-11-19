package vba_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
	"github.com/AOShei/go-fast-ooxml/internal/vba"
)

// The example of [MS-OVBA] 3.2.1: a chunk with nothing to compress.
func TestDecompressExample(t *testing.T) {
	in := []byte{0x01, 0x19, 0xB0, 0x00, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x00, 0x69, 0x6A, 0x6B, 0x6C,
		0x6D, 0x6E, 0x6F, 0x70, 0x00, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x2E}
	out, err := vba.Decompress(in, 1<<20)
	if err != nil || string(out) != "abcdefghijklmnopqrstuv." {
		t.Fatalf("got %q, %v", out, err)
	}
	if got := ooxmltest.VBACompress([]byte("abcdefghijklmnopqrstuv.")); !bytes.Equal(got, in) {
		t.Errorf("the test compressor gives % x", got)
	}
}

func TestDecompressRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	inputs := [][]byte{
		[]byte("#aaabcdefaaaaghijaaaabcdefaaaaghijaaaa"),
		bytes.Repeat([]byte("a"), 10000),
		[]byte(strings.Repeat("Sub Hello()\r\n    MsgBox \"Hello\"\r\nEnd Sub\r\n", 300)),
	}
	random := make([]byte, 9000) // does not compress: chunks stored as they are
	for i := range random {
		random[i] = byte(r.Uint32())
	}
	inputs = append(inputs, random)
	for _, in := range inputs {
		packed := ooxmltest.VBACompress(in)
		out, err := vba.Decompress(packed, 1<<20)
		if err != nil || !bytes.Equal(out, in) {
			t.Errorf("%d bytes: round trip gives %d bytes, %v", len(in), len(out), err)
		}
		// A limit cuts it short, and says so.
		if len(in) <= 100 {
			continue
		}
		out, err = vba.Decompress(packed, 100)
		if len(out) != 100 || err == nil || !bytes.Equal(out, in[:100]) {
			t.Errorf("%d bytes, limit 100: %d bytes, %v", len(in), len(out), err)
		}
	}
}

func TestDecompressDamage(t *testing.T) {
	good := ooxmltest.VBACompress([]byte(strings.Repeat("abcabcabc", 50)))
	for i := 0; i < len(good); i++ {
		for _, b := range []byte{0, 0xFF, good[i] ^ 0x10} {
			bad := append([]byte(nil), good...)
			bad[i] = b
			out, _ := vba.Decompress(bad, 1<<20)
			if len(out) > 4096*2 {
				t.Fatalf("byte %d: %d bytes from one chunk", i, len(out))
			}
		}
	}
	if _, err := vba.Decompress([]byte{2, 0, 0}, 100); err == nil {
		t.Error("a container that does not start with 1 is read")
	}
	// A copy token at the start of a chunk has nothing to copy.
	if _, err := vba.Decompress([]byte{1, 0x03, 0xB0, 0x01, 0x00, 0x00}, 100); err == nil {
		t.Error("a copy token with nothing before it is read")
	}
}

func TestProject(t *testing.T) {
	data := ooxmltest.VBAProject(1252,
		ooxmltest.VBAModule{Name: "Module1", Code: "Attribute VB_Name = \"Module1\"\r\nSub Hello()\r\n    MsgBox \"Caf\xe9 \x80\"\r\nEnd Sub\r\n"},
		ooxmltest.VBAModule{Name: "ThisWorkbook", Code: strings.Repeat("' a comment that repeats\r\n", 400)},
	)
	p, err := vba.Read(data, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if p.CodePage != 1252 || p.Damaged || p.Cut || p.Undecoded != 0 || len(p.Modules) != 2 {
		t.Fatalf("%+v", p)
	}
	if m := p.Modules[0]; m.Name != "Module1" || m.Code != "Attribute VB_Name = \"Module1\"\nSub Hello()\n    MsgBox \"Café €\"\nEnd Sub\n" {
		t.Errorf("%+v", m)
	}
	if m := p.Modules[1]; m.Name != "ThisWorkbook" || m.Code != strings.Repeat("' a comment that repeats\n", 400) {
		t.Errorf("%s: %d bytes", m.Name, len(m.Code))
	}
	// The limit is on all modules together.
	p, err = vba.Read(data, 200)
	if err != nil || !p.Cut || len(p.Modules) != 2 || len(p.Modules[0].Code)+len(p.Modules[1].Code) > 200 {
		t.Errorf("limit 200: %+v, %v", p, err)
	}
	// A code page with no table: what is not ASCII is U+FFFD.
	p, err = vba.Read(ooxmltest.VBAProject(1251, ooxmltest.VBAModule{Name: "M", Code: "' \xcf\xf0\xe8\xe2\xe5\xf2\r\n"}), 1<<20)
	if err != nil || p.Undecoded != 6 || p.Modules[0].Code != "' "+strings.Repeat("\uFFFD", 6)+"\n" {
		t.Errorf("1251: %+v, %v", p, err)
	}
	if _, err := vba.Read(ooxmltest.Compound(map[string][]byte{"x": []byte("y")}), 100); !errors.Is(err, vba.ErrNotProject) {
		t.Errorf("no project: %v", err)
	}
}

func FuzzProject(f *testing.F) {
	f.Add(ooxmltest.VBAProject(1252, ooxmltest.VBAModule{Name: "Module1", Code: "Sub A()\r\nEnd Sub\r\n"}))
	f.Add(ooxmltest.VBACompress([]byte("#aaabcdefaaaaghijaaaabcdefaaaaghijaaaa")))
	f.Fuzz(func(t *testing.T, data []byte) {
		if p, err := vba.Read(data, 1<<16); err == nil {
			n := 0
			for _, m := range p.Modules {
				n += len(m.Code)
			}
			// Decoding can make bytes longer: U+FFFD is three.
			if n > 3*(1<<16) {
				t.Fatalf("%d bytes of code from a limit of %d", n, 1<<16)
			}
		}
		vba.Decompress(data, 1<<16)
	})
}
