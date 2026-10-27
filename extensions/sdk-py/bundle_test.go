package pysdk

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

func TestSourceReadable(t *testing.T) {
	for _, name := range BundledFiles() {
		data, err := Source.ReadFile(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded %s is empty", name)
		}
	}
	// The package init is 300 KB of text, so the bundle embeds it as pig_sdk/__init__.py.gz.
	if !slices.Contains(BundledFiles(), "pig_sdk/__init__.py.gz") {
		t.Fatal("BundledFiles must include the pig_sdk package init so the staged SDK is importable")
	}
	compressed, err := Source.ReadFile("pig_sdk/__init__.py.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(data), "class Extension") {
		t.Fatalf("embedded pig_sdk/__init__.py.gz is not the SDK module: err=%v", err)
	}
}

// A module the package imports at load must be staged: a staged SDK that lacks one fails to import in every Python extension.
func TestEveryPackageSourceIsBundled(t *testing.T) {
	bundled := BundledFiles()
	err := filepath.WalkDir("pig_sdk", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".py") {
			return nil
		}
		name := filepath.ToSlash(path)
		if _, err := os.Stat(path + ".gz"); err == nil {
			name += ".gz" // the bundle embeds the gzip of this file
		}
		if !slices.Contains(bundled, name) {
			t.Errorf("%s is not in BundledFiles; run go generate in extensions/sdk-py", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A compressed copy is the file it stands for: staging writes the decompressed text under the name without .gz.
func TestCompressedCopiesHoldTheirFilesText(t *testing.T) {
	found := 0
	err := filepath.WalkDir("pig_sdk", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".py.gz") {
			return err
		}
		found++
		source, err := os.ReadFile(strings.TrimSuffix(path, ".gz"))
		if err != nil {
			return err
		}
		embedded, err := Source.ReadFile(filepath.ToSlash(path))
		if err != nil {
			return err
		}
		reader, err := gzip.NewReader(bytes.NewReader(embedded))
		if err != nil {
			return err
		}
		text, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		if !bytes.Equal(text, source) {
			t.Errorf("embedded %s differs from %s", path, strings.TrimSuffix(path, ".gz"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no compressed copy found: the unicode tables are embedded as _tables.py.gz")
	}
}
