//go:build windows

package runtimecell

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRenameRetryableClassifiesWindowsErrors(t *testing.T) {
	link := func(err error) error { return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: err} }
	for name, err := range map[string]error{
		"access denied":     link(windows.ERROR_ACCESS_DENIED),
		"sharing violation": link(windows.ERROR_SHARING_VIOLATION),
		"lock violation":    link(windows.ERROR_LOCK_VIOLATION),
		"EPERM":             link(syscall.EPERM),
		"EACCES":            link(syscall.EACCES),
		"EBUSY":             link(syscall.EBUSY),
		"bare errno":        windows.ERROR_ACCESS_DENIED,
		"wrapped":           errors.Join(errors.New("context"), link(windows.ERROR_SHARING_VIOLATION)),
	} {
		if !renameRetryable(err) {
			t.Errorf("%s: renameRetryable = false, want a retry", name)
		}
	}
	for name, err := range map[string]error{
		"nil":             nil,
		"file not found":  link(windows.ERROR_FILE_NOT_FOUND),
		"path not found":  link(windows.ERROR_PATH_NOT_FOUND),
		"exists":          link(windows.ERROR_ALREADY_EXISTS),
		"disk full":       link(windows.ERROR_DISK_FULL),
		"not same device": link(windows.ERROR_NOT_SAME_DEVICE),
		"ENOENT":          link(syscall.ENOENT),
		"plain":           errors.New("access is denied"),
	} {
		if renameRetryable(err) {
			t.Errorf("%s: renameRetryable = true, want the error reported unchanged", name)
		}
	}
}

// holdOpen opens path the way Go (and so a PiG process) does, which does not
// share delete access, and releases it when the test ends or release runs.
func holdOpen(t *testing.T, path string) (release func()) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = file.Close() }) }
	t.Cleanup(release)
	return release
}

// An open handle to any file inside a directory makes NTFS refuse to rename
// that directory with an access or sharing error. Anti-virus scanners and
// indexers hold freshly written files this way for a short, unpredictable time.
func TestWindowsRenamingADirectoryWithAHeldFileFailsWithARetryableError(t *testing.T) {
	root := t.TempDir()
	scratch := filepath.Join(root, ".build-x")
	if err := os.MkdirAll(filepath.Join(scratch, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(scratch, "runtime", "cell.mjs")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := holdOpen(t, file)
	err := os.Rename(scratch, filepath.Join(root, "final"))
	if err == nil {
		t.Fatal("rename of a directory containing an open file succeeded; the lock technique does not reproduce the report")
	}
	if !renameRetryable(err) {
		t.Fatalf("rename error %v is not classified as retryable", err)
	}
	release()
	if err := os.Rename(scratch, filepath.Join(root, "final")); err != nil {
		t.Fatalf("rename after the handle closed: %v", err)
	}
}

// Issue #106: publication failed with "publish cell entry: rename ...: Access is
// denied" while a transient handle was open. The lock clears shortly, so
// publication must wait it out and succeed.
func TestWindowsPublishArtifactWaitsOutAHandleHeldInScratch(t *testing.T) {
	final := filepath.Join(t.TempDir(), "cells", "node", "deadbeef")
	identity := EntryIdentity{InputDigest: "deadbeef", Artifact: "runner", Language: "node"}
	entry, err := PublishArtifact(t.Context(), final, "runner", "deadbeef", "node", func(scratch string) (string, error) {
		runtimeDir := filepath.Join(scratch, "runtime")
		if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
			return "", err
		}
		held := filepath.Join(runtimeDir, "cell.mjs")
		if err := os.WriteFile(held, []byte("x"), 0o644); err != nil {
			return "", err
		}
		file, err := os.Open(held)
		if err != nil {
			return "", err
		}
		time.AfterFunc(600*time.Millisecond, func() { _ = file.Close() })
		runner := filepath.Join(scratch, "runner")
		return runner, os.WriteFile(runner, []byte("launcher"), 0o644)
	}, "runtime")
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}
	art, ok := ValidEntry(final, identity)
	if !ok || entry.ArtifactPath != art || entry.Reused {
		t.Fatalf("entry = %+v (valid %v at %q), want a fresh published entry", entry, ok, art)
	}
	if _, err := os.Stat(filepath.Join(final, "runtime", "cell.mjs")); err != nil {
		t.Fatalf("published runtime tree: %v", err)
	}
}
