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

// Node runtime and module source trees cross file systems, so a link failure other than a refusal (EXDEV) must also copy, unlike Duplicate.
func TestLinkOrCopyCopiesWhateverFailsTheLink(t *testing.T) {
	for _, errno := range []unix.Errno{unix.EXDEV, unix.EMLINK, unix.EPERM, unix.EACCES} {
		t.Run(errno.Error(), func(t *testing.T) {
			refuseLinks(t, errno)
			strictUmask(t)
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "node"), filepath.Join(dir, "copy")
			writeFile(t, src, "runtime")
			if err := os.Chmod(src, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := LinkOrCopy(src, dst); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, dst); got != "runtime" {
				t.Fatalf("dst = %q, want %q", got, "runtime")
			}
			if info, err := os.Stat(dst); err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("dst mode = %v (err %v) under umask 077, want src's 0755", info.Mode().Perm(), err)
			}
		})
	}
}

func TestDuplicateDoesNotCopyWhenTheLinkFailsForAnotherReason(t *testing.T) {
	refuseLinks(t, unix.EXDEV)
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "pig"), filepath.Join(dir, ".backup")
	writeFile(t, src, "old")
	if err := Duplicate(src, dst); !errors.Is(err, unix.EXDEV) {
		t.Fatalf("Duplicate after EXDEV = %v, want EXDEV", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dst after a failed duplicate: %v, want it absent", err)
	}
}

// Duplicate's rollback copy must survive a crash, so it syncs. LinkOrCopy stages trees of thousands of files, where a sync per file doubles the staging time, so it does not sync, as its callers' copies never did.
func TestOnlyDuplicateSyncsItsCopy(t *testing.T) {
	refuseLinks(t, refusedLinkErrnos[0])
	original := syncFile
	t.Cleanup(func() { syncFile = original })
	var synced []string
	syncFile = func(f *os.File) error {
		synced = append(synced, filepath.Base(f.Name()))
		return original(f)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	writeFile(t, src, "body")
	if err := Duplicate(src, filepath.Join(dir, "duplicate")); err != nil {
		t.Fatal(err)
	}
	if err := LinkOrCopy(src, filepath.Join(dir, "staged")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "staged")); got != "body" {
		t.Fatalf("staged = %q, want %q", got, "body")
	}
	if len(synced) != 1 || synced[0] != "duplicate" {
		t.Fatalf("synced copies = %v, want only [duplicate]", synced)
	}
}
