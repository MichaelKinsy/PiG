package fspublish

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestDuplicateKeepsTheSourceAndAddsTheCopy(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "pig"), filepath.Join(dir, ".backup")
	writeFile(t, src, "old")
	if err := Duplicate(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, src); got != "old" {
		t.Fatalf("src = %q, want %q", got, "old")
	}
	if got := readFile(t, dst); got != "old" {
		t.Fatalf("dst = %q, want %q", got, "old")
	}
}

func TestDuplicateNeverReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "pig"), filepath.Join(dir, ".backup")
	writeFile(t, src, "old")
	writeFile(t, dst, "other")
	if err := Duplicate(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Duplicate over an existing file = %v, want an exist error", err)
	}
	if got := readFile(t, dst); got != "other" {
		t.Fatalf("dst = %q after a refused duplicate, want %q", got, "other")
	}
}
