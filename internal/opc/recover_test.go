package opc

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

// cutBeforeDirectory returns the archive without its directory.
func cutBeforeDirectory(t testing.TB, data []byte) []byte {
	t.Helper()
	end := bytes.LastIndex(data, []byte("PK\x05\x06"))
	if end < 0 {
		t.Fatal("no end record")
	}
	return data[:binary.LittleEndian.Uint32(data[end+16:])]
}

// readAll reads every member of a recovered archive to its end, or to
// where it breaks off.
func readAll(t testing.TB, zr *zip.Reader) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

// The members of an archive are found by their own headers in each of
// the forms a member's length is written in: in its header, or after its
// data, with a signature or without.
func TestRecoverFindsMembers(t *testing.T) {
	long := strings.Repeat("some text that compresses, ", 400)
	contents := map[string]string{"[Content_Types].xml": types, "word/document.xml": long, "empty.bin": "", "word/media/stored.bin": "stored as it is"}
	order := []string{"[Content_Types].xml", "word/document.xml", "empty.bin", "word/media/stored.bin"}
	build := func(streamed bool) []byte {
		return rawZip(t, func(w *zip.Writer) {
			for _, name := range order {
				h := &zip.FileHeader{Name: name, Method: zip.Deflate}
				if strings.HasSuffix(name, "stored.bin") {
					h.Method = zip.Store
				}
				if streamed {
					// The length follows the data.
					f, err := w.CreateHeader(h)
					if err != nil {
						t.Fatal(err)
					}
					io.WriteString(f, contents[name])
					continue
				}
				// The length is in the header.
				var raw bytes.Buffer
				body := []byte(contents[name])
				h.UncompressedSize64 = uint64(len(body))
				if h.Method == zip.Deflate {
					fw, _ := flateWriter(&raw)
					fw.Write(body)
					fw.Close()
				} else {
					raw.Write(body)
				}
				h.CompressedSize64 = uint64(raw.Len())
				h.CRC32 = crcOf(body)
				f, err := w.CreateRaw(h)
				if err != nil {
					t.Fatal(err)
				}
				f.Write(raw.Bytes())
			}
		})
	}
	for name, data := range map[string][]byte{"lengths in the headers": build(false), "lengths after the data": build(true)} {
		cut := cutBeforeDirectory(t, data)
		zr, rec, err := recoverArchive(bytes.NewReader(cut), int64(len(cut)), Limits{})
		if name == "lengths after the data" {
			// A member stored as it is, with its length after it, cannot
			// be told from what follows: the members before it are
			// found.
			if err != nil || rec.Members != 3 || rec.Cut != "" {
				t.Errorf("%s: %+v, %v", name, rec, err)
			}
			continue
		}
		if err != nil || rec.Members != len(order) || rec.Cut != "" {
			t.Fatalf("%s: %+v, %v", name, rec, err)
		}
		got := readAll(t, zr)
		for _, member := range order {
			if got[member] != contents[member] {
				t.Errorf("%s: %s reads as %d bytes, want %d", name, member, len(got[member]), len(contents[member]))
			}
		}
		// Cut inside the second member: the first is whole, the second
		// breaks off and says so when it is read.
		at := bytes.Index(data, []byte("word/document.xml")) + 60
		zr, rec, err = recoverArchive(bytes.NewReader(data[:at]), int64(at), Limits{})
		if err != nil || rec.Members != 2 || rec.Cut != "word/document.xml" {
			t.Fatalf("%s, cut in a member: %+v, %v", name, rec, err)
		}
		rc, err := zr.File[1].Open()
		if err != nil {
			t.Fatal(err)
		}
		part, err := io.ReadAll(rc)
		if err == nil || !strings.HasPrefix(long, string(part)) {
			t.Errorf("%s: the member that breaks off reads as %d bytes, %v", name, len(part), err)
		}
	}
}

// FuzzRecover gives the recovery arbitrary bytes behind the first four
// of an archive. Whatever they are, it returns in time proportional to
// them, stays within the budget, and what it returns can be read.
func FuzzRecover(f *testing.F) {
	whole := rawZip(f, func(w *zip.Writer) {
		for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
			fw, _ := w.Create(name)
			io.WriteString(fw, types+strings.Repeat("x", 300))
		}
	})
	cut := cutBeforeDirectory(f, whole)
	f.Add(cut)
	f.Add(cut[:len(cut)/2])
	f.Add(cut[:40])
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, data []byte) {
		lim := Limits{MaxDecodedBytes: 1 << 20, MaxMembers: 64}
		zr, rec, err := recoverArchive(bytes.NewReader(data), int64(len(data)), lim)
		if err != nil {
			if zr != nil || rec != nil {
				t.Fatal("a result with an error")
			}
			return
		}
		if rec.Members != len(zr.File) || rec.Members == 0 || rec.Members > 64 {
			t.Fatalf("%d members reported, %d in the directory", rec.Members, len(zr.File))
		}
		for _, f := range zr.File {
			if off, err := f.DataOffset(); err != nil || off > int64(len(data)) || off+int64(f.CompressedSize64) > int64(len(data)) {
				t.Fatalf("member %q: data at %d, %d bytes, in a file of %d (%v)", f.Name, off, f.CompressedSize64, len(data), err)
			}
			if rc, err := f.Open(); err == nil {
				io.Copy(io.Discard, io.LimitReader(rc, 2<<20))
				rc.Close()
			}
		}
		// And through Open, as a package.
		if p, err := Open(bytes.NewReader(data), int64(len(data)), lim); err == nil && p.Recovered() == nil && !bytes.Contains(data, []byte("PK\x05\x06")) {
			t.Fatal("opened without a directory and without recovery")
		}
	})
}

func flateWriter(w io.Writer) (io.WriteCloser, error) {
	return flate.NewWriter(w, flate.DefaultCompression)
}

func crcOf(b []byte) uint32 { return crc32.ChecksumIEEE(b) }
