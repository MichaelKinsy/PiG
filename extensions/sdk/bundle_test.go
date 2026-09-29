// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sdk

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestBundledFilesCoversModule fails when a source file is added to the SDK
// module without being added to BundledFiles. Staging an incomplete set would
// write a `package sdk` that does not compile, silently breaking every
// out-of-tree Go extension build. bundle.go itself is excluded by design.
func TestBundledFilesCoversModule(t *testing.T) {
	var want []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == "bundle.go" || strings.HasSuffix(path, "_test.go") {
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
	got := BundledFiles()
	slices.Sort(want)
	gotSorted := slices.Clone(got)
	slices.Sort(gotSorted)
	if !slices.Equal(want, gotSorted) {
		t.Fatalf("BundledFiles() out of sync with module sources:\n  embedded: %v\n  on disk:  %v\nadd/remove files in bundle.go's //go:embed and BundledFiles()", gotSorted, want)
	}
}

// TestSourceReadable verifies every bundled file is actually embedded and
// non-empty, so a stager can round-trip it to disk.
func TestSourceReadable(t *testing.T) {
	for _, name := range BundledFiles() {
		data, err := Source.ReadFile(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded %s is empty", name)
		}
		if name == "go.mod" && !strings.Contains(string(data), "module github.com/MichaelKinsy/PiG/extensions/sdk") {
			t.Fatalf("embedded go.mod is not the SDK module: %q", filepath.Base(name))
		}
	}
}
