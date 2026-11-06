// SPDX-License-Identifier: MIT

package sdk

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestBundledFilesCoversModule fails when a source file is added to the SDK
// module without being added to BundledFiles. Staging an incomplete set would
// write a `package sdk` that does not compile, silently breaking every
// out-of-tree Go extension build. bundle.go itself is excluded by design, as is
// the generated bundle_pack.go.
//
// That the frame holds these files' text, and that StagedHash is their hash, is
// checked where the frame can be decoded: TestEmbeddedBundlesHoldTheirSources in
// coding/extension/pigsdk (this module has no dependencies to decode it with).
func TestBundledFilesCoversModule(t *testing.T) {
	var want []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "testdata" {
			return filepath.SkipDir
		}
		if entry.IsDir() || path == "bundle.go" || path == "bundle_pack.go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if path == "go.mod" || entry.Name() == "LICENSE" || strings.HasSuffix(path, ".go") {
			want = append(want, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if got := BundledFiles(); !slices.Equal(want, got) {
		t.Fatalf("BundledFiles() out of sync with module sources:\n  embedded: %v\n  on disk:  %v\nrun go generate in extensions/sdk to rewrite bundle_pack.go and sdk.zst", got, want)
	}
}

// TestSourceHoldsTheFrame verifies the bundle is one non-empty file and names what a stager needs.
func TestSourceHoldsTheFrame(t *testing.T) {
	entries, err := fs.ReadDir(Source, ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "sdk.zst" {
		t.Fatalf("Source holds %v (%v), want the one file sdk.zst", entries, err)
	}
	frame, err := Source.ReadFile("sdk.zst")
	if err != nil || len(frame) == 0 {
		t.Fatalf("sdk.zst: %d bytes, %v", len(frame), err)
	}
	// a zstd frame starts with its magic number
	if !slices.Equal(frame[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		t.Fatalf("sdk.zst starts %x, not a zstd frame", frame[:4])
	}
	for _, required := range []string{"go.mod", "LICENSE"} {
		if !slices.Contains(BundledFiles(), required) {
			t.Fatalf("BundledFiles must include %s so the staged module builds", required)
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(StagedHash()) {
		t.Fatalf("StagedHash() = %q, want 16 hex digits", StagedHash())
	}
}

// BundledFiles hands out a copy: a caller that sorts or edits it leaves the list the stager reads alone.
func TestBundledFilesReturnsACopy(t *testing.T) {
	files := BundledFiles()
	first := files[0]
	files[0] = "changed"
	if BundledFiles()[0] != first {
		t.Fatal("BundledFiles shares its list with callers")
	}
}
