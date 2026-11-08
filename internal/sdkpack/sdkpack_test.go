// SPDX-License-Identifier: MIT

package sdkpack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

type file struct {
	name string
	data []byte
}

func TestWalkReturnsWhatAppendPacked(t *testing.T) {
	files := []file{{"LICENSE", []byte("MIT\n")}, {"empty", nil}, {"src/a.rs", bytes.Repeat([]byte{0, 1, 2}, 100)}}
	var blob []byte
	for _, f := range files {
		blob = Append(blob, f.name, f.data)
	}
	var got []file
	if err := Walk(blob, func(name string, data []byte) error {
		got = append(got, file{name, data})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(files) {
		t.Fatalf("walked %d files, packed %d", len(got), len(files))
	}
	for i, f := range files {
		if got[i].name != f.name || !bytes.Equal(got[i].data, f.data) {
			t.Errorf("file %d = %q (%d bytes), want %q (%d bytes)", i, got[i].name, len(got[i].data), f.name, len(f.data))
		}
		if cap(got[i].data) != len(got[i].data) {
			t.Errorf("%s has capacity %d for %d bytes: an append would write into the next file", f.name, cap(got[i].data), len(got[i].data))
		}
	}
}

// Hash is the digest pigsdk has always named staged SDKs by.
func TestHashIsTheStagingDigest(t *testing.T) {
	files := []file{{"a.go", []byte("package a\n")}, {"b/c.go", []byte("package c\n")}}
	var blob []byte
	digest := sha256.New()
	digest.Write([]byte("staging-v2\x00"))
	for _, f := range files {
		blob = Append(blob, f.name, f.data)
		_, _ = fmt.Fprintf(digest, "%s\x00%d\x00", f.name, len(f.data))
		digest.Write(f.data)
	}
	if want := hex.EncodeToString(digest.Sum(nil))[:16]; Hash(blob) != want {
		t.Fatalf("Hash = %s, want %s", Hash(blob), want)
	}
}

func TestWalkRejectsMalformedBlobs(t *testing.T) {
	good := Append(nil, "a.txt", []byte("hello"))
	for name, blob := range map[string][]byte{
		"truncated data":  good[:len(good)-1],
		"no size":         []byte("a.txt\x00"),
		"empty name":      []byte("\x005\x00hello"),
		"bad size":        []byte("a.txt\x00x\x00hello"),
		"huge size":       []byte("a.txt\x0099999999999\x00hello"),
		"absolute name":   Append(nil, "/etc/passwd", nil),
		"escaping name":   Append(nil, "../x", nil),
		"trailing header": append(bytes.Clone(good), "b.txt"...),
	} {
		if err := Walk(blob, func(string, []byte) error { return nil }); err == nil {
			t.Errorf("%s: walked", name)
		}
	}
}
