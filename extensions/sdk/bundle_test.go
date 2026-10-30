// SPDX-License-Identifier: MIT

package sdk

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
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
//
// A bundled file is embedded as its gzip, so the set it must equal is the
// module's files named for what the bundle embeds: path + ".gz".
func TestBundledFilesCoversModule(t *testing.T) {
	var want []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "testdata" {
			return filepath.SkipDir
		}
		if entry.IsDir() || path == "bundle.go" || path == "bundle_embed.go" || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".gz") {
			return nil
		}
		if path == "go.mod" || entry.Name() == "LICENSE" || strings.HasSuffix(path, ".go") {
			want = append(want, embeddedName(filepath.ToSlash(path)))
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
		t.Fatalf("BundledFiles() out of sync with module sources:\n  embedded: %v\n  on disk:  %v\nrun go generate in extensions/sdk to rewrite bundle_embed.go", gotSorted, want)
	}
}

// embeddedName is the name the bundle embeds path under: path + ".gz" when a compressed copy of it sits beside it, which holds the same text, otherwise path.
func embeddedName(path string) string {
	if hasCompressedCopy(path) {
		return path + ".gz"
	}
	return path
}

// hasCompressedCopy reports whether the bundle embeds a file as its gzip, path + ".gz", which holds the same text.
func hasCompressedCopy(path string) bool {
	_, err := os.Stat(path + ".gz")
	return err == nil
}

// embeddedText is the file the stager writes for an embedded name: the text of a .gz name's gzip, the bytes of any other.
func embeddedText(name string) ([]byte, error) {
	data, err := Source.ReadFile(name)
	if err != nil || !strings.HasSuffix(name, ".gz") {
		return data, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

// A compressed copy is the file it stands for: staging writes the decompressed text under the name without .gz. Run go generate in extensions/sdk after
// editing a file the bundle embeds compressed.
func TestCompressedCopiesHoldTheirFilesText(t *testing.T) {
	found := 0
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "testdata" {
			return filepath.SkipDir
		}
		if entry.IsDir() || !hasCompressedCopy(path) {
			return nil
		}
		found++
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text, err := embeddedText(filepath.ToSlash(path) + ".gz")
		if err != nil {
			return err
		}
		if !bytes.Equal(text, source) {
			t.Errorf("embedded %s.gz differs from %s; run go generate in extensions/sdk", path, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no compressed copy found: every source file is embedded gzipped")
	}
}

// TestSourceReadable verifies every bundled file is actually embedded and
// non-empty once expanded, so a stager can round-trip it to disk.
func TestSourceReadable(t *testing.T) {
	for _, name := range BundledFiles() {
		data, err := embeddedText(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded %s is empty", name)
		}
		if strings.TrimSuffix(name, ".gz") == "go.mod" && !strings.Contains(string(data), "module github.com/MichaelKinsy/PiG/extensions/sdk") {
			t.Fatalf("embedded go.mod is not the SDK module: %q", filepath.Base(name))
		}
	}
	for _, required := range []string{"go.mod", "LICENSE"} {
		if !slices.Contains(BundledFiles(), embeddedName(required)) {
			t.Fatalf("BundledFiles must include %s so the staged module builds", required)
		}
	}
}
