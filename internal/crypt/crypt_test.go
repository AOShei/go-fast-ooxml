package crypt_test

import (
	"bytes"
	"context"
	"crypto"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AOShei/go-fast-ooxml/internal/crypt"
	"github.com/AOShei/go-fast-ooxml/internal/ooxmltest"
)

func content(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// decrypt opens what ooxmltest.EncryptStreams made, with the password.
func decrypt(info, encrypted []byte, password string) (*crypt.Info, *crypt.Package, error) {
	in, err := crypt.Parse(bytes.NewReader(info), int64(len(info)))
	if err != nil {
		return nil, nil, err
	}
	key, err := in.Key(context.Background(), password, 0)
	if err != nil {
		return in, nil, err
	}
	pkg, err := in.Open(bytes.NewReader(encrypted), int64(len(encrypted)), key)
	return in, pkg, err
}

// Every scheme, key length and hash this package reads, on packages of
// the lengths where things could go wrong: empty, around a cipher block,
// around a segment.
func TestPackagesAreDecrypted(t *testing.T) {
	var hows []ooxmltest.Encryption
	for _, bits := range []int{128, 192, 256} {
		hows = append(hows, ooxmltest.Encryption{Standard: true, KeyBits: bits})
		for _, h := range []string{"SHA1", "SHA256", "SHA384", "SHA512"} {
			hows = append(hows, ooxmltest.Encryption{KeyBits: bits, Hash: h, SpinCount: 50})
		}
	}
	rng := rand.New(rand.NewSource(3))
	for _, how := range hows {
		for _, size := range []int{0, 1, 15, 16, 17, 4095, 4096, 4097, 8192, 70_001} {
			if how.Standard && size > 4097 && how.KeyBits != 128 {
				continue // the key takes 50,000 rounds each time; one length of key is enough for the long ones
			}
			name := fmt.Sprintf("%+v, %d bytes", how, size)
			want := content(size, int64(size))
			how.Seed = int64(size)
			info, encrypted := ooxmltest.EncryptStreams(want, "pass wörd \U0001F511", how)
			in, pkg, err := decrypt(info, encrypted, "pass wörd \U0001F511")
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if in.Agile() == how.Standard || pkg.Size() != int64(size) {
				t.Errorf("%s: agile %v, %d bytes", name, in.Agile(), pkg.Size())
			}
			got := make([]byte, size)
			if n, err := pkg.ReadAt(got, 0); n != size || err != nil && err != io.EOF || !bytes.Equal(got, want) {
				t.Errorf("%s: read %d bytes (%v), and they are not the package", name, n, err)
			}
			// Any piece, at any offset: a zip archive is read so.
			for i := 0; i < 40 && size > 0; i++ {
				off := rng.Intn(size)
				n := 1 + rng.Intn(min(size-off, 10_000))
				buf := make([]byte, n)
				if got, err := pkg.ReadAt(buf, int64(off)); got != n || err != nil || !bytes.Equal(buf, want[off:off+n]) {
					t.Errorf("%s: %d bytes at %d: %d read, %v", name, n, off, got, err)
					break
				}
			}
			buf := make([]byte, 8)
			if n, err := pkg.ReadAt(buf, int64(size)-2); size >= 2 && (n != 2 || err != io.EOF) {
				t.Errorf("%s: reading over the end: %d, %v", name, n, err)
			}
			// The wrong password is refused before anything is decrypted.
			if _, _, err := decrypt(info, encrypted, "pass word"); !errors.Is(err, crypt.ErrPassword) {
				t.Errorf("%s: the wrong password: %v", name, err)
			}
			if size != 4096 {
				continue
			}
			// The integrity value of the agile scheme holds for the stream
			// as it was written and for no other.
			key, _ := in.Key(context.Background(), "pass wörd \U0001F511", 0)
			ok, checked, err := in.Intact(context.Background(), bytes.NewReader(encrypted), int64(len(encrypted)), key)
			if err != nil || checked == how.Standard || checked && !ok {
				t.Errorf("%s: intact %v, checked %v, %v", name, ok, checked, err)
			}
			changed := append([]byte(nil), encrypted...)
			changed[len(changed)/2] ^= 1
			if ok, checked, _ := in.Intact(context.Background(), bytes.NewReader(changed), int64(len(changed)), key); checked && ok {
				t.Errorf("%s: a changed stream passes for intact", name)
			}
			if ok, checked, _ := in.Intact(context.Background(), bytes.NewReader(encrypted[:len(encrypted)-16]), int64(len(encrypted)), key); checked && ok {
				t.Errorf("%s: a stream cut short passes for intact", name)
			}
		}
	}
}

func TestPasswords(t *testing.T) {
	pkg := content(5000, 1)
	for _, how := range []ooxmltest.Encryption{{Standard: true}, {SpinCount: 10}} {
		for _, password := range []string{"", "a", crypt.DefaultPassword, strings.Repeat("long ", 100), "é中\U0001F600", "tr\x00ailing"} {
			info, encrypted := ooxmltest.EncryptStreams(pkg, password, how)
			if _, _, err := decrypt(info, encrypted, password); err != nil {
				t.Errorf("%+v, password %q: %v", how, password, err)
			}
			if _, _, err := decrypt(info, encrypted, password+"x"); !errors.Is(err, crypt.ErrPassword) {
				t.Errorf("%+v, password %q with a letter more: %v", how, password, err)
			}
		}
	}
	// A file without the integrity value is not checked, and says so.
	info, encrypted := ooxmltest.EncryptStreams(pkg, "p", ooxmltest.Encryption{SpinCount: 10, NoIntegrity: true})
	in, _, err := decrypt(info, encrypted, "p")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := in.Key(context.Background(), "p", 0)
	if _, checked, err := in.Intact(context.Background(), bytes.NewReader(encrypted), int64(len(encrypted)), key); checked || err != nil {
		t.Errorf("no integrity value: checked %v, %v", checked, err)
	}
}

// What a guess at the password costs is the file's to say. A caller
// bounds it, and a guess can be given up.
func TestSpinCount(t *testing.T) {
	info, _ := ooxmltest.EncryptStreams([]byte("x"), "p", ooxmltest.Encryption{SpinCount: 5000})
	in, err := crypt.Parse(bytes.NewReader(info), int64(len(info)))
	if err != nil {
		t.Fatal(err)
	}
	if in.SpinCount() != 5000 || in.String() != "agile, AES-256, SHA512" {
		t.Errorf("%d rounds, %s", in.SpinCount(), in)
	}
	if _, err := in.Key(context.Background(), "p", 4999); !errors.Is(err, crypt.ErrSpinCount) {
		t.Errorf("over the limit: %v", err)
	}
	if _, err := in.Key(context.Background(), "p", 5000); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	standard, _ := ooxmltest.EncryptStreams([]byte("x"), "p", ooxmltest.Encryption{Standard: true, KeyBits: 192})
	in, _ = crypt.Parse(bytes.NewReader(standard), int64(len(standard)))
	if in.SpinCount() != 50000 || in.String() != "standard, AES-192, SHA1" {
		t.Errorf("%d rounds, %s", in.SpinCount(), in)
	}
	if _, err := in.Key(context.Background(), "p", 49999); !errors.Is(err, crypt.ErrSpinCount) {
		t.Errorf("the standard scheme over the limit: %v", err)
	}

	// A file that asks for a thousand million rounds, and a caller who
	// stops waiting.
	huge := bytes.Replace(info, []byte(`spinCount="5000"`), []byte(`spinCount="1000000000"`), 1)
	in, err = crypt.Parse(bytes.NewReader(huge), int64(len(huge)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := in.Key(ctx, "p", 0); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Errorf("giving up: %v after %v", err, time.Since(start))
	}
}

func TestEncryptionInfoThatCannotBeRead(t *testing.T) {
	agile, _ := ooxmltest.EncryptStreams([]byte("x"), "p", ooxmltest.Encryption{SpinCount: 10})
	standard, _ := ooxmltest.EncryptStreams([]byte("x"), "p", ooxmltest.Encryption{Standard: true})
	swap := func(b []byte, old, new string) []byte {
		if !bytes.Contains(b, []byte(old)) {
			t.Fatalf("%q is not in the encryption information", old)
		}
		return bytes.Replace(b, []byte(old), []byte(new), -1)
	}
	set := func(b []byte, at int, v ...byte) []byte {
		out := append([]byte(nil), b...)
		copy(out[at:], v)
		return out
	}
	cases := []struct {
		name string
		info []byte
		want error
	}{
		{"empty", nil, crypt.ErrMalformed},
		{"seven bytes", agile[:7], crypt.ErrMalformed},
		{"a version of the binary formats", set(standard, 0, 1, 0, 1, 0), crypt.ErrUnsupported},
		{"extensible encryption", set(standard, 0, 4, 0, 3, 0), crypt.ErrUnsupported},
		{"standard, cut in the header", standard[:40], crypt.ErrMalformed},
		{"standard, cut in the verifier", standard[:len(standard)-10], crypt.ErrMalformed},
		{"standard, a header larger than the stream", set(standard, 8, 0xFF, 0xFF, 0, 0), crypt.ErrMalformed},
		{"standard, a header of no size", set(standard, 8, 0, 0, 0, 0), crypt.ErrMalformed},
		{"standard, not AES", set(standard, 4, 0x04, 0, 0, 0), crypt.ErrUnsupported},
		{"standard, a provider outside the file", set(standard, 4, 0x34, 0, 0, 0), crypt.ErrUnsupported},
		{"standard, an unknown cipher", set(standard, 12+8, 0x01, 0x68, 0, 0), crypt.ErrUnsupported},
		{"standard, an unknown hash", set(standard, 12+12, 0x03, 0x80, 0, 0), crypt.ErrUnsupported},
		{"standard, a key of the wrong length", set(standard, 12+16, 0x00, 0x01, 0, 0), crypt.ErrMalformed},
		{"agile, not XML", append(append([]byte(nil), agile[:8]...), "<encryption><keyData"...), crypt.ErrMalformed},
		{"agile, empty XML", append(append([]byte(nil), agile[:8]...), "<encryption/>"...), crypt.ErrMalformed},
		{"agile, another cipher", swap(agile, `cipherAlgorithm="AES"`, `cipherAlgorithm="DES"`), crypt.ErrUnsupported},
		{"agile, another chaining mode", swap(agile, "ChainingModeCBC", "ChainingModeCFB"), crypt.ErrUnsupported},
		{"agile, another hash", swap(agile, `hashAlgorithm="SHA512"`, `hashAlgorithm="WHIRLPOOL"`), crypt.ErrUnsupported},
		{"agile, a hash of the wrong size", swap(agile, `hashSize="64"`, `hashSize="20"`), crypt.ErrMalformed},
		{"agile, a key of the wrong length", swap(agile, `keyBits="256"`, `keyBits="100"`), crypt.ErrMalformed},
		{"agile, blocks of the wrong size", swap(agile, `blockSize="16"`, `blockSize="8"`), crypt.ErrMalformed},
		{"agile, a salt that is not what it says", swap(agile, `saltSize="16"`, `saltSize="32"`), crypt.ErrMalformed},
		{"agile, a number that is not one", swap(agile, `spinCount="10"`, `spinCount="ten"`), crypt.ErrMalformed},
		{"agile, a negative number", swap(agile, `spinCount="10"`, `spinCount="-5"`), crypt.ErrMalformed},
		{"agile, a value that is not base64", swap(agile, `encryptedKeyValue="`, `encryptedKeyValue="!!`), crypt.ErrMalformed},
		{"agile, a value that is not whole blocks", swap(agile, `encryptedKeyValue="`, `encryptedKeyValue="QUJD" x="`), crypt.ErrMalformed},
		{"agile, no key for a password", swap(agile, "spinCount=", "certificate="), crypt.ErrUnsupported},
		{"agile, no key data", swap(agile, "<keyData", "<other"), crypt.ErrMalformed},
	}
	for _, c := range cases {
		in, err := crypt.Parse(bytes.NewReader(c.info), int64(len(c.info)))
		if in != nil || !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := crypt.Parse(bytes.NewReader(make([]byte, 2<<20)), 2<<20); !errors.Is(err, crypt.ErrMalformed) {
		t.Errorf("two megabytes of encryption information: %v", err)
	}
}

// A stream that is shorter than the package it says it holds.
func TestEncryptedPackageCutShort(t *testing.T) {
	want := content(20_000, 1)
	for _, how := range []ooxmltest.Encryption{{Standard: true}, {SpinCount: 10}} {
		info, encrypted := ooxmltest.EncryptStreams(want, "p", how)
		in, _ := crypt.Parse(bytes.NewReader(info), int64(len(info)))
		key, err := in.Key(context.Background(), "p", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{0, 7} {
			if _, err := in.Open(bytes.NewReader(encrypted[:n]), int64(n), key); !errors.Is(err, crypt.ErrMalformed) {
				t.Errorf("%+v: a stream of %d bytes: %v", how, n, err)
			}
		}
		// The stream says 20,000 bytes and holds fewer: opening it says
		// so, since the length is known.
		if _, err := in.Open(bytes.NewReader(encrypted[:9000]), 9000, key); !errors.Is(err, crypt.ErrMalformed) {
			t.Errorf("%+v: a stream cut short: %v", how, err)
		}
		// The stream is as long as it says and the file under it is not:
		// what can be read is, and the rest is an error, not zeros.
		pkg, err := in.Open(bytes.NewReader(encrypted[:9000]), int64(len(encrypted)), key)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 20_000)
		n, err := pkg.ReadAt(buf, 0)
		if !errors.Is(err, crypt.ErrMalformed) || n > 9000 || n < 4096 || !bytes.Equal(buf[:n], want[:n]) {
			t.Errorf("%+v: reading past where the stream ends: %d bytes, %v", how, n, err)
		}
		if _, err := pkg.ReadAt(buf, -1); err == nil {
			t.Errorf("%+v: a negative offset is read", how)
		}
	}
}

// A decrypted package is read by many goroutines at once, as the sheets
// of a workbook are.
func TestPackageIsSafeForConcurrentUse(t *testing.T) {
	want := content(300_000, 1)
	for _, how := range []ooxmltest.Encryption{{Standard: true}, {SpinCount: 10}} {
		info, encrypted := ooxmltest.EncryptStreams(want, "p", how)
		_, pkg, err := decrypt(info, encrypted, "p")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(g)))
				for i := 0; i < 200; i++ {
					off := rng.Intn(len(want))
					n := 1 + rng.Intn(min(len(want)-off, 20_000))
					buf := make([]byte, n)
					if got, err := pkg.ReadAt(buf, int64(off)); got != n || err != nil || !bytes.Equal(buf, want[off:off+n]) {
						t.Errorf("%d bytes at %d: %d read, %v", n, off, got, err)
						return
					}
				}
			}()
		}
		wg.Wait()
	}
}

func FuzzEncryptionInfo(f *testing.F) {
	agile, encrypted := ooxmltest.EncryptStreams(content(5000, 1), "p", ooxmltest.Encryption{SpinCount: 3})
	standard, _ := ooxmltest.EncryptStreams([]byte("x"), "p", ooxmltest.Encryption{Standard: true})
	f.Add(agile)
	f.Add(standard)
	f.Add([]byte{4, 0, 4, 0, 0x40, 0, 0, 0})
	alice := people()["Alice"]
	cert, _ := ooxmltest.EncryptStreams(content(5000, 1), "p", ooxmltest.Encryption{SpinCount: 3, Certificates: []ooxmltest.Recipient{{Cert: alice.cert}, {Cert: people()["Bob"].cert, OAEP: true}}})
	f.Add(cert)
	f.Fuzz(func(t *testing.T, info []byte) {
		in, err := crypt.Parse(bytes.NewReader(info), int64(len(info)))
		if err != nil {
			if in != nil || !errors.Is(err, crypt.ErrMalformed) && !errors.Is(err, crypt.ErrUnsupported) {
				t.Fatalf("an error of no known kind: %v", err)
			}
			return
		}
		_ = in.String()
		_ = in.Subjects()
		// A private key is tried, then a password.
		if key, _, err := in.CertificateKey([]crypto.Decrypter{alice.key}); err == nil {
			if pkg, err := in.Open(bytes.NewReader(encrypted), int64(len(encrypted)), key); err == nil {
				pkg.ReadAt(make([]byte, 100), 0)
			}
		} else if !errors.Is(err, crypt.ErrNoKey) {
			t.Fatalf("an error of no known kind: %v", err)
		}
		// A password is tried, within a bound on what that may cost.
		key, err := in.Key(context.Background(), "p", 200)
		if err != nil {
			if !errors.Is(err, crypt.ErrPassword) && !errors.Is(err, crypt.ErrSpinCount) && !errors.Is(err, crypt.ErrMalformed) {
				t.Fatalf("an error of no known kind: %v", err)
			}
			return
		}
		pkg, err := in.Open(bytes.NewReader(encrypted), int64(len(encrypted)), key)
		if err != nil {
			return
		}
		buf := make([]byte, pkg.Size())
		pkg.ReadAt(buf, 0)
		in.Intact(context.Background(), bytes.NewReader(encrypted), int64(len(encrypted)), key)
	})
}
