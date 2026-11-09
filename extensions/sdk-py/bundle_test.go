package pysdk

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The bundle is the one file sdk.zst, and names what a stager needs. That the frame holds these files' text and StagedHash is their hash is checked where it can be decoded: TestEmbeddedBundlesHoldTheirSources in coding/extension/pigsdk.
func TestSourceHoldsTheFrame(t *testing.T) {
	entries, err := fs.ReadDir(Source, ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "sdk.zst" {
		t.Fatalf("Source holds %v (%v), want the one file sdk.zst", entries, err)
	}
	frame, err := Source.ReadFile("sdk.zst")
	if err != nil || len(frame) == 0 {
		t.Fatalf("sdk.zst: %d bytes, %v", len(frame), err)
	}
	if !slices.Contains(BundledFiles(), "pig_sdk/__init__.py") {
		t.Fatal("BundledFiles must include the pig_sdk package init so the staged SDK is importable")
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(StagedHash()) {
		t.Fatalf("StagedHash() = %q, want 16 hex digits", StagedHash())
	}
}

// A module the package imports at load must be staged: a staged SDK that lacks one fails to import in every Python extension.
func TestEveryPackageSourceIsBundled(t *testing.T) {
	bundled := BundledFiles()
	var onDisk []string
	err := filepath.WalkDir("pig_sdk", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".py") {
			return nil
		}
		name := filepath.ToSlash(path)
		onDisk = append(onDisk, name)
		if !slices.Contains(bundled, name) {
			t.Errorf("%s is not in BundledFiles; run go generate in extensions/sdk-py", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// and nothing else: the list is the package's modules, the license and pyproject.toml
	want := append(slices.Clone(onDisk), "LICENSE", "pyproject.toml")
	slices.Sort(want)
	if !slices.Equal(bundled, want) {
		t.Errorf("BundledFiles = %v, sources = %v; run go generate in extensions/sdk-py", bundled, want)
	}
	for _, name := range []string{"LICENSE", "pyproject.toml"} {
		if _, err := os.Stat(name); err != nil {
			t.Error(err)
		}
	}
}
