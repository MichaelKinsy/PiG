// SPDX-License-Identifier: MIT

package sdkpack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
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

// A root-module SDK directory carries its sources and one frame, sdk.zst, and nothing else packed: the gzip twins (.gz/) and the tar (sdk.tgz) that earlier bundle formats wrote are not read by any bundle, are not written by packsdk, and count against the module zip (automation/release/module-size-budget.txt). A merge from a branch that predates the single frame brought them back once (about 460 KB). The nested extensions/sdk module keeps its own .gz twins, which its published version embeds.
func TestRootSDKDirectoriesHoldNoSupersededBundleFormat(t *testing.T) {
	for _, dir := range []string{"sdk-lisp", "sdk-py", "sdk-rs"} {
		root := filepath.Join("..", "..", "extensions", dir)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := entry.Name()
			if entry.IsDir() {
				if name == "target" || name == "__pycache__" || name == "node_modules" {
					return filepath.SkipDir
				}
				if name == ".gz" {
					t.Errorf("%s: superseded gzip twin directory; the bundle is sdk.zst alone", path)
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".tar.gz") {
				t.Errorf("%s: superseded tar bundle; the bundle is sdk.zst alone", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
