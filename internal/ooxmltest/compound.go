package ooxmltest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash"
	"math/rand"
	"sort"
	"strings"
	"unicode/utf16"
)

// This file writes what the library only reads: compound files, and
// packages encrypted with a password. It is written from the published
// descriptions of the formats (MS-CFB, MS-OFFCRYPTO), on its own, so
// that a test which encrypts with it and decrypts with the library
// checks one reading of the description against another. The library's
// reading is also held to files that Microsoft Office and LibreOffice
// wrote and to another program that decrypts them.

// Compound returns a compound file holding the given streams. A name
// with slashes puts the stream in storages: "a/b" is stream b in storage
// a. Streams under 4096 bytes are kept as short streams, as the format
// has it.
func Compound(streams map[string][]byte) []byte { return compound(streams, 9) }

// CompoundV4 is Compound in the format's later version, with sectors of
// 4096 bytes.
func CompoundV4(streams map[string][]byte) []byte { return compound(streams, 12) }

func compound(streams map[string][]byte, shift int) []byte {
	sector := 1 << shift
	perSector := sector / 4
	const (
		short      = 64
		cutoff     = 4096
		free       = 0xFFFFFFFF
		endOfChain = 0xFFFFFFFE
		fatSector  = 0xFFFFFFFD
		difSector  = 0xFFFFFFFC
		none       = 0xFFFFFFFF
	)
	// The directory: the root, then every storage and stream, each
	// storage's children in name order, linked from one to the next.
	type node struct {
		name     string
		kind     byte
		data     []byte
		children map[string]*node
		index    int
		start    uint32
	}
	root := &node{name: "Root Entry", kind: 5, children: map[string]*node{}}
	for path, data := range streams {
		at := root
		parts := strings.Split(path, "/")
		for i, name := range parts {
			child := at.children[name]
			if child == nil {
				child = &node{name: name, kind: 1, children: map[string]*node{}}
				at.children[name] = child
			}
			if i == len(parts)-1 {
				child.kind, child.data = 2, data
			}
			at = child
		}
	}
	var entries []*node
	var number func(n *node)
	number = func(n *node) {
		n.index = len(entries)
		entries = append(entries, n)
		names := make([]string, 0, len(n.children))
		for name := range n.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			number(n.children[name])
		}
	}
	number(root)
	sorted := func(n *node) []*node {
		out := make([]*node, 0, len(n.children))
		for _, c := range n.children {
			out = append(out, c)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].index < out[j].index })
		return out
	}

	// Short streams go into one container, a short sector at a time.
	var container []byte
	var miniFAT []uint32
	for _, n := range entries {
		if n.kind != 2 || len(n.data) == 0 || len(n.data) >= cutoff {
			continue
		}
		n.start = uint32(len(miniFAT))
		count := (len(n.data) + short - 1) / short
		for i := 0; i < count; i++ {
			next := uint32(len(miniFAT) + 1)
			if i == count-1 {
				next = endOfChain
			}
			miniFAT = append(miniFAT, next)
		}
		container = append(container, n.data...)
		container = append(container, make([]byte, count*short-len(n.data))...)
	}

	// The sectors, in order: the allocation table's own, the list of
	// those beyond the 109 the header holds, the directory, the short
	// table, the container, then the long streams.
	sectorsOf := func(n int) int { return (n + sector - 1) / sector }
	dirSectors := sectorsOf(len(entries) * 128)
	miniFATSectors := sectorsOf(len(miniFAT) * 4)
	data := dirSectors + miniFATSectors + sectorsOf(len(container))
	for _, n := range entries {
		if n.kind == 2 && len(n.data) >= cutoff {
			data += sectorsOf(len(n.data))
		}
	}
	fatSectors, difSectors := 0, 0
	for {
		need := (data + fatSectors + difSectors + perSector - 1) / perSector
		dif := 0
		if need > 109 {
			dif = (need - 109 + perSector - 2) / (perSector - 1)
		}
		if need == fatSectors && dif == difSectors {
			break
		}
		fatSectors, difSectors = need, dif
	}
	fat := make([]uint32, fatSectors*perSector)
	for i := range fat {
		fat[i] = free
	}
	next := 0
	take := func(n int, mark uint32) uint32 {
		first := uint32(next)
		for i := 0; i < n; i++ {
			switch {
			case mark != 0:
				fat[next] = mark
			case i == n-1:
				fat[next] = endOfChain
			default:
				fat[next] = uint32(next + 1)
			}
			next++
		}
		if n == 0 {
			return endOfChain
		}
		return first
	}
	firstFAT := take(fatSectors, fatSector)
	firstDIF := take(difSectors, difSector)
	firstDir := take(dirSectors, 0)
	firstMiniFAT := take(miniFATSectors, 0)
	root.start = take(sectorsOf(len(container)), 0)
	root.data = container
	for _, n := range entries {
		if n.kind == 2 && len(n.data) >= cutoff {
			n.start = take(sectorsOf(len(n.data)), 0)
		}
	}

	le := binary.LittleEndian
	out := make([]byte, sector*(1+next))
	at := func(n uint32) []byte { return out[sector*(1+int(n)):] }
	head := out[:512]
	copy(head, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	le.PutUint16(head[24:], 0x3E)
	le.PutUint16(head[26:], 3)
	if shift == 12 {
		le.PutUint16(head[26:], 4)
		le.PutUint32(head[40:], uint32(dirSectors)) // this version states it
	}
	le.PutUint16(head[28:], 0xFFFE)
	le.PutUint16(head[30:], uint16(shift))
	le.PutUint16(head[32:], 6)
	le.PutUint32(head[44:], uint32(fatSectors))
	le.PutUint32(head[48:], firstDir)
	le.PutUint32(head[56:], cutoff)
	le.PutUint32(head[60:], firstMiniFAT)
	le.PutUint32(head[64:], uint32(miniFATSectors))
	le.PutUint32(head[68:], firstDIF)
	le.PutUint32(head[72:], uint32(difSectors))
	for i := 0; i < 109; i++ {
		v := uint32(free)
		if i < fatSectors {
			v = firstFAT + uint32(i)
		}
		le.PutUint32(head[76+4*i:], v)
	}
	// The table sectors past the first 109 are listed in sectors that
	// each end with the number of the next.
	for i := 0; i < difSectors; i++ {
		sec := at(firstDIF + uint32(i))
		for k := 0; k < perSector-1; k++ {
			v, which := uint32(free), 109+i*(perSector-1)+k
			if which < fatSectors {
				v = firstFAT + uint32(which)
			}
			le.PutUint32(sec[4*k:], v)
		}
		link := uint32(endOfChain)
		if i < difSectors-1 {
			link = firstDIF + uint32(i) + 1
		}
		le.PutUint32(sec[sector-4:], link)
	}
	for i, v := range fat {
		le.PutUint32(at(firstFAT)[4*i:], v)
	}
	for i, v := range miniFAT {
		le.PutUint32(at(firstMiniFAT)[4*i:], v)
	}
	for i := len(miniFAT); i < miniFATSectors*perSector; i++ {
		le.PutUint32(at(firstMiniFAT)[4*i:], free)
	}
	if len(container) > 0 {
		copy(at(root.start), container)
	}
	for _, n := range entries {
		if n.kind == 2 && len(n.data) >= cutoff {
			copy(at(n.start), n.data)
		}
	}
	dir := at(firstDir)
	for i, n := range entries {
		e := dir[128*i : 128*i+128]
		units := utf16.Encode([]rune(n.name))
		for k, u := range units {
			le.PutUint16(e[2*k:], u)
		}
		le.PutUint16(e[64:], uint16(2*len(units)+2))
		e[66], e[67] = n.kind, 1
		child := uint32(none)
		if kids := sorted(n); len(kids) > 0 {
			child = uint32(kids[0].index)
		}
		le.PutUint32(e[68:], none) // no left sibling: see below
		le.PutUint32(e[76:], child)
		le.PutUint32(e[116:], n.start)
		le.PutUint64(e[120:], uint64(len(n.data)))
	}
	// Each storage's children are a chain: every child's right sibling
	// is the next in name order. That is a search tree, if a lopsided
	// one.
	for _, n := range entries {
		kids := sorted(n)
		for k, c := range kids {
			right := uint32(none)
			if k+1 < len(kids) {
				right = uint32(kids[k+1].index)
			}
			le.PutUint32(dir[128*c.index+72:], right)
		}
	}
	le.PutUint32(dir[72:], none) // the root has no sibling
	for i := len(entries); i < dirSectors*(sector/128); i++ {
		e := dir[128*i : 128*i+128]
		le.PutUint32(e[68:], none)
		le.PutUint32(e[72:], none)
		le.PutUint32(e[76:], none)
	}
	return out
}

// Encryption says how Encrypt encrypts a package.
type Encryption struct {
	// Standard selects the scheme of Office 2007; the default is the
	// agile scheme of Office 2010 and later.
	Standard bool
	// KeyBits is 128, 192 or 256; zero means 256 for agile and 128 for
	// standard.
	KeyBits int
	// Hash is SHA1, SHA256, SHA384 or SHA512 (agile only; default
	// SHA512).
	Hash string
	// SpinCount is the number of rounds of hashing (agile only; default
	// 1000, so that tests are quick; Office uses 100,000).
	SpinCount int
	// Seed makes the salts and keys the same on every run.
	Seed int64
	// NoIntegrity leaves the integrity check out (agile only).
	NoIntegrity bool
}

// Encrypt returns the package encrypted with the password: a compound
// file with the EncryptionInfo and EncryptedPackage streams, as Office
// writes one.
func Encrypt(pkg []byte, password string, how Encryption) []byte {
	info, encrypted := EncryptStreams(pkg, password, how)
	return Compound(map[string][]byte{
		"EncryptionInfo":   info,
		"EncryptedPackage": encrypted,
		// What Office puts beside them to say how the package is
		// transformed. Nothing in it is needed to decrypt.
		"\x06DataSpaces/Version": []byte("\x3c\x00\x00\x00M\x00i\x00c\x00r\x00o\x00s\x00o\x00f\x00t\x00"),
	})
}

// EncryptStreams returns the two streams of an encrypted package.
func EncryptStreams(pkg []byte, password string, how Encryption) (info, encrypted []byte) {
	rng := rand.New(rand.NewSource(how.Seed + 1))
	random := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	le := binary.LittleEndian
	units := utf16.Encode([]rune(password))
	pw := make([]byte, 2*len(units))
	for i, u := range units {
		le.PutUint16(pw[2*i:], u)
	}
	spin := func(h hash.Hash, salt []byte, rounds int) []byte {
		h.Reset()
		h.Write(salt)
		h.Write(pw)
		sum := h.Sum(nil)
		for i := 0; i < rounds; i++ {
			var n [4]byte
			le.PutUint32(n[:], uint32(i))
			h.Reset()
			h.Write(n[:])
			h.Write(sum)
			sum = h.Sum(nil)
		}
		return sum
	}
	padded := func(b []byte, to int) []byte {
		return append(append([]byte(nil), b...), make([]byte, (to-len(b)%to)%to)...)
	}
	size := make([]byte, 8)
	le.PutUint64(size, uint64(len(pkg)))

	if how.Standard {
		keyBits := how.KeyBits
		if keyBits == 0 {
			keyBits = 128
		}
		salt := random(16)
		h := sha1.New()
		sum := spin(h, salt, 50000)
		h.Reset()
		h.Write(sum)
		h.Write([]byte{0, 0, 0, 0})
		final := h.Sum(nil)
		var key []byte
		for _, pad := range []byte{0x36, 0x5C} {
			buf := bytes.Repeat([]byte{pad}, 64)
			for i, b := range final {
				buf[i] ^= b
			}
			x := sha1.Sum(buf)
			key = append(key, x[:]...)
		}
		block, _ := aes.NewCipher(key[:keyBits/8])
		ecb := func(b []byte) []byte {
			out := padded(b, 16)
			for i := 0; i < len(out); i += 16 {
				block.Encrypt(out[i:i+16], out[i:i+16])
			}
			return out
		}
		verifier := random(16)
		sumV := sha1.Sum(verifier)
		alg := map[int]uint32{128: 0x660E, 192: 0x660F, 256: 0x6610}[keyBits]
		var header bytes.Buffer
		for _, v := range []uint32{0x24, 0, alg, 0x8004, uint32(keyBits), 0x18, 0, 0} {
			binary.Write(&header, le, v)
		}
		for _, u := range utf16.Encode([]rune("Microsoft Enhanced RSA and AES Cryptographic Provider\x00")) {
			binary.Write(&header, le, u)
		}
		var out bytes.Buffer
		binary.Write(&out, le, []uint16{4, 2})
		binary.Write(&out, le, []uint32{0x24, uint32(header.Len())})
		out.Write(header.Bytes())
		binary.Write(&out, le, uint32(16))
		out.Write(salt)
		out.Write(ecb(verifier))
		binary.Write(&out, le, uint32(20))
		out.Write(ecb(sumV[:]))
		return out.Bytes(), append(size, ecb(pkg)...)
	}

	keyBits, name, rounds := how.KeyBits, how.Hash, how.SpinCount
	if keyBits == 0 {
		keyBits = 256
	}
	if name == "" {
		name = "SHA512"
	}
	if rounds == 0 {
		rounds = 1000
	}
	newHash := map[string]func() hash.Hash{"SHA1": sha1.New, "SHA256": sha256.New, "SHA384": sha512.New384, "SHA512": sha512.New}[name]
	h := newHash()
	fit := func(b []byte, n int) []byte {
		out := bytes.Repeat([]byte{0x36}, n)
		copy(out, b)
		return out
	}
	sumOf := func(parts ...[]byte) []byte {
		h.Reset()
		for _, p := range parts {
			h.Write(p)
		}
		return h.Sum(nil)
	}
	cbc := func(key, iv, plain []byte) []byte {
		block, _ := aes.NewCipher(key)
		out := padded(plain, 16)
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, out)
		return out
	}
	dataSalt, keySalt := random(16), random(16)
	secret := random(keyBits / 8)
	spunSum := spin(h, keySalt, rounds)
	derive := func(purpose []byte) []byte { return fit(sumOf(spunSum, purpose), keyBits/8) }
	verifier := random(16)
	encVerifierIn := cbc(derive([]byte{0xfe, 0xa7, 0xd2, 0x76, 0x3b, 0x4b, 0x9e, 0x79}), keySalt, verifier)
	encVerifierIs := cbc(derive([]byte{0xd7, 0xaa, 0x0f, 0x6d, 0x30, 0x61, 0x34, 0x4e}), keySalt, sumOf(verifier))
	encKey := cbc(derive([]byte{0x14, 0x6e, 0x0b, 0xe7, 0xab, 0xac, 0xd0, 0xd6}), keySalt, secret)

	encrypted = size
	for i := 0; i*4096 < len(pkg); i++ {
		var n [4]byte
		le.PutUint32(n[:], uint32(i))
		encrypted = append(encrypted, cbc(secret, fit(sumOf(dataSalt, n[:]), 16), pkg[i*4096:min(len(pkg), i*4096+4096)])...)
	}
	integrity := ""
	if !how.NoIntegrity {
		macKey := random(h.Size())
		mac := hmac.New(newHash, macKey)
		mac.Write(encrypted)
		encMacKey := cbc(secret, fit(sumOf(dataSalt, []byte{0x5f, 0xb2, 0xad, 0x01, 0x0c, 0xb9, 0xe1, 0xf6}), 16), macKey)
		encMacValue := cbc(secret, fit(sumOf(dataSalt, []byte{0xa0, 0x67, 0x7f, 0x02, 0xb2, 0x2c, 0x84, 0x33}), 16), mac.Sum(nil))
		integrity = fmt.Sprintf(`<dataIntegrity encryptedHmacKey="%s" encryptedHmacValue="%s"/>`, base64.StdEncoding.EncodeToString(encMacKey), base64.StdEncoding.EncodeToString(encMacValue))
	}
	b64 := base64.StdEncoding.EncodeToString
	common := fmt.Sprintf(`saltSize="16" blockSize="16" keyBits="%d" hashSize="%d" cipherAlgorithm="AES" cipherChaining="ChainingModeCBC" hashAlgorithm="%s"`, keyBits, h.Size(), name)
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\r\n" +
		`<encryption xmlns="http://schemas.microsoft.com/office/2006/encryption" xmlns:p="http://schemas.microsoft.com/office/2006/keyEncryptor/password">` +
		`<keyData ` + common + ` saltValue="` + b64(dataSalt) + `"/>` + integrity +
		`<keyEncryptors><keyEncryptor uri="http://schemas.microsoft.com/office/2006/keyEncryptor/password">` +
		fmt.Sprintf(`<p:encryptedKey spinCount="%d" `, rounds) + common + ` saltValue="` + b64(keySalt) + `"` +
		` encryptedVerifierHashInput="` + b64(encVerifierIn) + `" encryptedVerifierHashValue="` + b64(encVerifierIs) + `" encryptedKeyValue="` + b64(encKey) + `"/>` +
		`</keyEncryptor></keyEncryptors></encryption>`
	info = append([]byte{4, 0, 4, 0, 0x40, 0, 0, 0}, xml...)
	return info, encrypted
}
