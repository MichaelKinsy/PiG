package rssdk

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// embeddedName is the name the bundle embeds path under: .gz/path.gz when a compressed copy of it sits in the .gz directory, which holds the same text, otherwise path.
func embeddedName(path string) string {
	if hasCompressedCopy(path) {
		return ".gz/" + path + ".gz"
	}
	return path
}

// stagedPath is the name the stager writes an embedded name under: the name a .gz/ name holds the gzip of, the name itself for any other.
func stagedPath(name string) string {
	if rest, compressed := strings.CutPrefix(name, ".gz/"); compressed {
		return strings.TrimSuffix(rest, ".gz")
	}
	return name
}

// embeddedText is the file the stager writes for an embedded name: the text of the gzip a name under .gz/ holds, the bytes of any other.
func embeddedText(name string) ([]byte, error) {
	data, err := Source.ReadFile(name)
	if err != nil || stagedPath(name) == name {
		return data, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

func TestSourceReadable(t *testing.T) {
	for _, name := range BundledFiles() {
		data, err := embeddedText(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded %s is empty", name)
		}
	}
	for _, required := range []string{"Cargo.toml", "src/lib.rs", "src/login.rs"} {
		if !slices.Contains(BundledFiles(), embeddedName(required)) {
			t.Fatalf("BundledFiles must include %s so the staged crate builds", required)
		}
	}
	data, err := embeddedText(embeddedName("Cargo.toml"))
	if err != nil || !strings.Contains(string(data), "pig-sdk") {
		t.Fatalf("embedded Cargo.toml is not the SDK crate: err=%v", err)
	}
}

// The staged crate builds only if every module source is bundled: lib.rs
// declares each src/*.rs file as a module.
func TestBundledFilesIncludeEveryCrateSource(t *testing.T) {
	err := filepath.WalkDir("src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name := filepath.ToSlash(path)
		if isTestModule(name) {
			return nil
		}
		name = embeddedName(name)
		if !slices.Contains(BundledFiles(), name) {
			t.Errorf("BundledFiles is missing crate source %s", name)
		}
		if _, err := Source.ReadFile(name); err != nil {
			t.Errorf("crate source %s is not embedded: %v", name, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
