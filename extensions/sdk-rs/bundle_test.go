package rssdk

import (
	"os"
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
	for _, required := range []string{"Cargo.toml", "src/lib.rs", "src/login.rs"} {
		if !slices.Contains(BundledFiles(), required) {
			t.Fatalf("BundledFiles must include %s so the staged crate builds", required)
		}
	}
	data, err := Source.ReadFile("Cargo.toml")
	if err != nil || !strings.Contains(string(data), "pig-sdk") {
		t.Fatalf("embedded Cargo.toml is not the SDK crate: err=%v", err)
	}
}

// The staged crate builds only if every module source is bundled: lib.rs
// declares each src/*.rs file as a module.
func TestBundledFilesIncludeEveryCrateSource(t *testing.T) {
	entries, err := os.ReadDir("src")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := "src/" + entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".rs") {
			continue
		}
		if !slices.Contains(BundledFiles(), name) {
			t.Errorf("BundledFiles is missing crate source %s", name)
		}
		if _, err := Source.ReadFile(name); err != nil {
			t.Errorf("crate source %s is not embedded: %v", name, err)
		}
	}
}
