package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func TestCopySessionDirectoryCopiesPrivatelyAndLinksBlobs(t *testing.T) {
	data := t.TempDir()
	from := filepath.Join(data, "sessions", "src")
	write(t, filepath.Join(from, "store.sqlite"), "store", 0o644)
	write(t, filepath.Join(from, "store.sqlite-wal"), "wal", 0o644)
	write(t, filepath.Join(from, "files", "index.json"), "index", 0o644)
	write(t, filepath.Join(from, "files", "blobs", "ab", "blob"), "blob", 0o400)
	write(t, filepath.Join(from, "files", ".uploads", "partial"), "partial", 0o600)
	if err := os.Symlink("/etc/passwd", filepath.Join(from, "link")); err != nil {
		t.Fatal(err)
	}

	if err := CopySessionDirectory(context.Background(), data, "src", "dst"); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(data, "sessions", "dst")
	for path, want := range map[string]string{"store.sqlite": "store", "store.sqlite-wal": "wal", "files/index.json": "index", "files/blobs/ab/blob": "blob"} {
		got, err := os.ReadFile(filepath.Join(to, path))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", path, got, err)
		}
	}
	// Directories are 0700 and files 0600 (a blob, never changed, is linked: the source's file).
	for _, path := range []string{".", "files"} {
		if info, _ := os.Stat(filepath.Join(to, path)); info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v", path, info.Mode().Perm())
		}
	}
	if info, _ := os.Stat(filepath.Join(to, "store.sqlite")); info.Mode().Perm() != 0o600 {
		t.Errorf("store mode = %v", info.Mode().Perm())
	}
	source, _ := os.Stat(filepath.Join(from, "files", "blobs", "ab", "blob"))
	copied, _ := os.Stat(filepath.Join(to, "files", "blobs", "ab", "blob"))
	if !os.SameFile(source, copied) {
		t.Error("a blob was copied, not linked")
	}
	// Uploads under way and links are left out.
	for _, path := range []string{"files/.uploads", "link"} {
		if _, err := os.Lstat(filepath.Join(to, path)); !os.IsNotExist(err) {
			t.Errorf("%s was copied: %v", path, err)
		}
	}
	// Nothing is overwritten.
	if err := CopySessionDirectory(context.Background(), data, "src", "dst"); err == nil {
		t.Error("copied over an existing session")
	}
}

func TestCopyOfASessionWithoutADirectoryIsNothing(t *testing.T) {
	data := t.TempDir()
	if err := CopySessionDirectory(context.Background(), data, "none", "dst"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "sessions", "dst")); !os.IsNotExist(err) {
		t.Fatalf("dst: %v", err)
	}
}

func TestCopyRefusesNamesThatAreNoSessionIDs(t *testing.T) {
	if err := CopySessionDirectory(context.Background(), t.TempDir(), "../x", "dst"); err == nil {
		t.Fatal("a path was taken as a session ID")
	}
}

func TestRemoveSessionCopyRemovesItsDirectoryAndUploads(t *testing.T) {
	data := t.TempDir()
	write(t, filepath.Join(data, "sessions", "dst", "store.sqlite"), "x", 0o600)
	write(t, filepath.Join(data, "uploads", "dst", "file"), "x", 0o600)
	write(t, filepath.Join(data, "sessions", "keep", "store.sqlite"), "x", 0o600)
	if err := RemoveSessionCopy(data, "dst"); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"sessions/dst", "uploads/dst"} {
		if _, err := os.Stat(filepath.Join(data, gone)); !os.IsNotExist(err) {
			t.Errorf("%s: %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "sessions", "keep")); err != nil {
		t.Errorf("another session's directory: %v", err)
	}
	// A directory that is not there is nothing to remove.
	if err := RemoveSessionCopy(data, "dst"); err != nil {
		t.Fatal(err)
	}
}
