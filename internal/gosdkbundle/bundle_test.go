// SPDX-License-Identifier: MIT

package gosdkbundle

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const moduleDir = "../../extensions/sdk"

// TestBundledFilesCoverTheModule fails when a source file is added to the SDK module without being added to the bundle. Staging an incomplete set would write a `package sdk` that does not compile, silently breaking every out-of-tree Go extension build. The module's bundle.go and bundle_embed.go are excluded by design.
//
// That the frame holds these files' text, and that StagedHash is their hash, is checked where the frame can be decoded: TestEmbeddedBundlesHoldTheirSources in coding/extension/pigsdk.
func TestBundledFilesCoverTheModule(t *testing.T) {
	var want []string
	err := filepath.WalkDir(moduleDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "testdata" || entry.Name() == ".gz") {
			return filepath.SkipDir
		}
		name, err := filepath.Rel(moduleDir, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if entry.IsDir() || name == "bundle.go" || name == "bundle_embed.go" || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gz") {
			return nil
		}
		if name == "go.mod" || entry.Name() == "LICENSE" || strings.HasSuffix(name, ".go") {
			want = append(want, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if got := BundledFiles(); !slices.Equal(want, got) {
		t.Fatalf("BundledFiles() out of sync with the module's sources:\n  bundled: %v\n  on disk: %v\nrun go generate in internal/gosdkbundle to rewrite bundle_pack.go and sdk.zst", got, want)
	}
}

// TestSourceHoldsTheFrame verifies the bundle is one non-empty zstd frame and names what a stager needs.
func TestSourceHoldsTheFrame(t *testing.T) {
	entries, err := fs.ReadDir(Source, ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "sdk.zst" {
		t.Fatalf("Source holds %v (%v), want the one file sdk.zst", entries, err)
	}
	frame, err := Source.ReadFile("sdk.zst")
	if err != nil || len(frame) < 4 || !slices.Equal(frame[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		t.Fatalf("sdk.zst: %d bytes, %v, not a zstd frame", len(frame), err)
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
