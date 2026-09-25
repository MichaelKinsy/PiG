// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sdk

import (
	"os"
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
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read module dir: %v", err)
	}
	want := []string{"LICENSE", "go.mod"}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == "bundle.go" {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if strings.HasSuffix(name, ".go") {
			want = append(want, name)
		}
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
