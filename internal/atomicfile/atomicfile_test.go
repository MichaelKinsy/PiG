//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noTemporaryFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", entry.Name())
		}
	}
}

// A file that does not exist is created with the mode asked for; one that exists is replaced whole and keeps its mode, whatever perm says.
func TestWriteFileCreatesPrivateAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("new file mode = %v, want 0600", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "second" {
		t.Fatalf("content = %q", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("rewritten file mode = %v, want the 0640 it had", info.Mode().Perm())
	}
	noTemporaryFile(t, dir)
}

// writeFileSync follows a symbolic link; the replacement must land on the target and leave the link.
func TestWriteFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "managed", "auth.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "auth.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", info, err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Fatalf("target = %q, want new", data)
	}
	noTemporaryFile(t, dir)
	noTemporaryFile(t, filepath.Dir(target))
}

// A file the caller may not write is refused, as an in-place write refuses it, with its content intact and no temporary file.
func TestWriteFileRefusesReadOnlyFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes any file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("kept"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("lost"), 0o600); !os.IsPermission(err) {
		t.Fatalf("WriteFile of a read-only file = %v, want a permission error", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "kept" {
		t.Fatalf("content = %q", data)
	}
	noTemporaryFile(t, dir)
}

// A directory that cannot take a new entry fails the write before any byte is written: the file stays as it was.
func TestWriteFileInReadOnlyDirectoryKeepsFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into any directory")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := WriteFile(path, []byte("lost"), 0o600); err == nil {
		t.Fatal("WriteFile in a read-only directory succeeded")
	}
	if data, _ := os.ReadFile(path); string(data) != "kept" {
		t.Fatalf("content = %q", data)
	}
}
