package modulehash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrackedFilesFailsOutsideGitCheckout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := TrackedFiles(dir)
	if err == nil {
		t.Fatalf("TrackedFiles(%q) outside git returned %v, want error", dir, files)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q does not name directory %q", err.Error(), dir)
	}
}

func TestTrackedFilesFailsWithoutGitOnPATH(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	files, err := TrackedFiles(dir)
	if err == nil {
		t.Fatalf("TrackedFiles(%q) without git on PATH returned %v, want error", dir, files)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q does not name directory %q", err.Error(), dir)
	}
}
