package xmlscan

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// tokens reads a stream through s, reset with Reset or ResetBytes, and
// writes down every token and attribute.
func tokens(s *Scanner, doc string, size int, bytes bool) string {
	if bytes {
		s.ResetBytes([]byte(doc), nil)
	} else {
		s.Reset(strings.NewReader(doc), &Options{BufferSize: size})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "before %q %q %d %q;", s.Name(), s.Local(), s.Space(), s.Text())
	for {
		k := s.Next()
		if k == EOF {
			break
		}
		fmt.Fprintf(&b, "%d %s %q;", k, s.Local(), s.Text())
		for i := range s.NumAttr() {
			_, l, v := s.AttrAt(i)
			fmt.Fprintf(&b, " %s=%q", l, v)
		}
	}
	fmt.Fprintf(&b, " err=%v", s.Err())
	return b.String()
}

func reusedDoc(tag string, n int) string {
	var b strings.Builder
	b.WriteString("<root>")
	for i := range n {
		fmt.Fprintf(&b, `<p id="%s%d">%s text %d &amp; more</p>`, tag, i, tag, i)
	}
	b.WriteString("</root>")
	return b.String()
}

// A scanner reset for another stream reads it as a new scanner does: no
// name or text of the stream before shows, before the first Next or on
// the first start tags.
func TestReusedScannerReadsAsNew(t *testing.T) {
	sizes := []int{4096, 5000, 16 << 10, 64 << 10}
	var s Scanner
	for i := range 120 {
		tag := string(rune('a' + i%4))
		n := []int{1, 50, 3000}[i%3]
		size := sizes[(i/3)%len(sizes)]
		bytes := i%5 == 0
		var fresh Scanner
		want := tokens(&fresh, reusedDoc(tag, n), size, bytes)
		if got := tokens(&s, reusedDoc(tag, n), size, bytes); got != want {
			cut := min(len(got), len(want), 120)
			t.Fatalf("stream %d (%s, %d elements, window %d, bytes %v) differs from a new scanner's:\nreused: %s\nnew:    %s", i, tag, n, size, bytes, got[:cut], want[:cut])
		}
	}
}

// Windows handed back by Release are taken by other scanners, on other
// goroutines: each stream still reads as a new scanner reads it, and a
// released scanner gives nothing more. Run with -race.
func TestReleasedWindowsAreReused(t *testing.T) {
	sizes := []int{4096, 5000, 16 << 10, 64 << 10, 100 << 10}
	want := map[string]string{}
	for _, tag := range []string{"a", "b", "c", "d"} {
		for _, n := range []int{1, 50, 3000} {
			for _, size := range sizes {
				var s Scanner
				want[fmt.Sprint(tag, n, size)] = tokens(&s, reusedDoc(tag, n), size, false)
			}
		}
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			var s Scanner
			for i := range 200 {
				tag := string(rune('a' + (g+i)%4))
				n := []int{1, 50, 3000}[(g*7+i)%3]
				size := sizes[(g+i*3)%len(sizes)]
				if got := tokens(&s, reusedDoc(tag, n), size, false); got != want[fmt.Sprint(tag, n, size)] {
					t.Errorf("goroutine %d, stream %d (%s, %d elements, window %d): read differs from a new scanner's", g, i, tag, n, size)
					return
				}
				s.Release()
				if k := s.Next(); k != EOF || s.Local() != nil || s.Text() != nil {
					t.Errorf("a released scanner gave %v %q %q", k, s.Local(), s.Text())
					return
				}
			}
		})
	}
	wg.Wait()
}
