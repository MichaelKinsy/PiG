package rssdk

import (
	"io/fs"
	"os"
	"slices"
	"testing"
)

// Every source file Cargo can consume must ship with the staged SDK, including newly added modules.
func TestBundledFilesMatchRustSourceTree(t *testing.T) {
	want := []string{"LICENSE", "Cargo.toml", "Cargo.lock"}
	err := fs.WalkDir(os.DirFS("."), "src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			want = append(want, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	got := slices.Clone(BundledFiles())
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("staged Rust sources=%v, want filesystem inventory=%v", got, want)
	}
	for _, path := range want {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := Source.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(source) != string(embedded) {
			t.Fatalf("staged Rust source %s differs from its current source", path)
		}
	}
}
