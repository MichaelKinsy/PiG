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

// strictUmask sets the umask Android app processes commonly run with, which would narrow a created file's mode.
func strictUmask(t *testing.T) {
	t.Helper()
	original := unix.Umask(0o077)
	t.Cleanup(func() { unix.Umask(original) })
}

func TestDuplicateCopiesWhenTheFileSystemRefusesLinks(t *testing.T) {
	for _, errno := range refusedLinkErrnos {
		t.Run(errno.Error(), func(t *testing.T) {
			refuseLinks(t, errno)
			strictUmask(t)
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "pig"), filepath.Join(dir, ".backup")
			writeFile(t, src, "old")
			if err := os.Chmod(src, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := Duplicate(src, dst); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, dst); got != "old" {
				t.Fatalf("dst = %q, want %q", got, "old")
			}
			info, err := os.Stat(dst)
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("dst mode = %v (err %v) under umask 077, want src's 0755", info.Mode().Perm(), err)
			}
			if got := readFile(t, src); got != "old" {
				t.Fatalf("src = %q, want it kept", got)
			}
		})
	}
}

func TestDuplicateCopyNeverReplacesAnExistingFile(t *testing.T) {
	refuseLinks(t, refusedLinkErrnos[0])
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

func TestDuplicateRemovesAFailedCopy(t *testing.T) {
	refuseLinks(t, refusedLinkErrnos[0])
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "not-a-file"), filepath.Join(dir, ".backup")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Duplicate(src, dst); err == nil {
		t.Fatal("Duplicate of a directory succeeded, want a read error")
	}
	if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dst after a failed copy: %v, want it removed", err)
	}
}
