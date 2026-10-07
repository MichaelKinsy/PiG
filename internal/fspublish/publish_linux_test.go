//go:build linux

package fspublish

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// refuseLinks makes every link fail as Android fails a hard link in an app's private data directory.
func refuseLinks(t *testing.T) {
	t.Helper()
	original := link
	link = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: unix.EACCES}
	}
	t.Cleanup(func() { link = original })
}

func TestPublishRenamesWhenTheFileSystemRefusesLinks(t *testing.T) {
	refuseLinks(t)
	dir := t.TempDir()
	stage, target := filepath.Join(dir, ".stage"), filepath.Join(dir, "target")
	writeFile(t, stage, "new")
	if err := Publish(stage, target); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != "new" {
		t.Fatalf("target = %q, want %q", got, "new")
	}
	if _, err := os.Stat(stage); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stage after the rename: %v, want it gone", err)
	}
}

func TestPublishRenameNeverReplacesAnExistingTarget(t *testing.T) {
	refuseLinks(t)
	dir := t.TempDir()
	stage, target := filepath.Join(dir, ".stage"), filepath.Join(dir, "target")
	writeFile(t, stage, "new")
	writeFile(t, target, "old")
	if err := Publish(stage, target); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Publish over an existing target = %v, want an exist error", err)
	}
	if got := readFile(t, target); got != "old" {
		t.Fatalf("target = %q after a refused publish, want %q", got, "old")
	}
	if got := readFile(t, stage); got != "new" {
		t.Fatalf("stage = %q after a refused publish, want it kept", got)
	}
}

func TestPublishReportsTheRefusedLinkWithoutRenameNoReplace(t *testing.T) {
	refuseLinks(t)
	original := renameat2
	renameat2 = func(int, string, int, string, uint) error { return unix.ENOSYS }
	t.Cleanup(func() { renameat2 = original })
	dir := t.TempDir()
	stage, target := filepath.Join(dir, ".stage"), filepath.Join(dir, "target")
	writeFile(t, stage, "new")
	err := Publish(stage, target)
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || linkErr.Op != "link" || !errors.Is(err, unix.EACCES) {
		t.Fatalf("Publish without RENAME_NOREPLACE = %v, want the refused link", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("target after a refused publish: %v, want none", statErr)
	}
}
