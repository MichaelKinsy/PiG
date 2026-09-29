//go:build !windows

package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledPigletClosureRejectsSourceAndDestinationSymlinks(t *testing.T) {
	source, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("not in closure"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, path := range []string{"link", "link/secret"} {
		closure := pigletClosure{root: root, prefix: "alpha.source/commit", files: map[string][]byte{}, modes: map[string]os.FileMode{}}
		if _, err := closure.copy(path); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("source %s=%v", path, err)
		}
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "piglets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "piglets", "alpha.source")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "piglets", "alpha.source", "commit", "secret")
	if err := commitPigletAddFiles(map[string][]byte{target: []byte("wrong")}, nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("destination=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "commit")); !os.IsNotExist(err) {
		t.Fatalf("escaped write=%v", err)
	}
}
