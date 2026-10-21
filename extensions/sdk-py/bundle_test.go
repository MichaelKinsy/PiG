package pysdk

import (
	"io/fs"
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
	if !slices.Contains(BundledFiles(), "pig_sdk/__init__.py") {
		t.Fatal("BundledFiles must include the pig_sdk package init so the staged SDK is importable")
	}
	data, err := Source.ReadFile("pig_sdk/__init__.py")
	if err != nil || !strings.Contains(string(data), "class Extension") {
		t.Fatalf("embedded pig_sdk/__init__.py is not the SDK module: err=%v", err)
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
		if !slices.Contains(bundled, filepath.ToSlash(path)) {
			t.Errorf("%s is not in BundledFiles", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
