package runtimecell

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A directory that a walk found and another process then removed held no source; one that is still there and cannot be read stays an error. Windows reports a directory being deleted as ERROR_DELETE_PENDING or ERROR_ACCESS_DENIED, which deletion stands in for here.
func TestDirectoryVanishedClassification(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "dir", Err: errors.New("access denied")}
	gone := &fs.PathError{Op: "lstat", Path: "dir", Err: fs.ErrNotExist}
	pendingErr := &fs.PathError{Op: "open", Path: "dir", Err: errors.New("delete pending")}
	deletion := func(err error) (pending, deniedCode bool) {
		switch {
		case errors.Is(err, pendingErr.Err):
			return true, false
		case errors.Is(err, denied.Err):
			return false, true
		}
		return false, false
	}
	exists := func() error { return nil }
	for _, tc := range []struct {
		name  string
		err   error
		lstat func() error
		want  bool
	}{
		{"not exist", &fs.PathError{Op: "open", Path: "dir", Err: fs.ErrNotExist}, exists, true},
		{"delete pending", pendingErr, exists, true},
		{"denied, directory gone", denied, func() error { return gone }, true},
		{"denied, directory still pending deletion", denied, func() error { return denied }, true},
		{"denied, directory there", denied, exists, false},
		{"other error, directory gone", errors.New("input/output error"), func() error { return gone }, false},
	} {
		if got := directoryVanished(tc.err, tc.lstat, deletion); got != tc.want {
			t.Errorf("%s: directoryVanished = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Without Windows' codes an access denial is only ever an unreadable directory.
	if directoryVanished(os.ErrPermission, func() error { return gone }, func(error) (bool, bool) { return false, false }) {
		t.Error("a permission error was taken for a vanished directory without a Windows deletion code")
	}
}

// The hash of an on-disk module is stable while a build creates and removes directories under a directory of the module that no CACHEDIR.TAG marks (cargo's rmeta temp directories before its target directory's tag exists, or any other tool's output).
func TestHashGoModuleIgnoresConcurrentChurnOfUntaggedDirectories(t *testing.T) {
	root := t.TempDir()
	for name, content := range goModuleFiles() {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deps := filepath.Join(root, "tmp", "test-fixtures", "rust-target", "release", "deps")
	if err := os.MkdirAll(deps, 0o755); err != nil {
		t.Fatal(err)
	}
	want := hashGoModule(root)
	if strings.HasPrefix(want, "error:") {
		t.Fatal(want)
	}
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			temp := filepath.Join(deps, "rmeta"+strconv.Itoa(i))
			if err := os.MkdirAll(temp, 0o755); err != nil {
				done <- err
				return
			}
			if err := os.WriteFile(filepath.Join(temp, "lib.rmeta"), []byte("x"), 0o644); err != nil {
				done <- err
				return
			}
			if err := os.RemoveAll(temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
				done <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := hashGoModule(root); got != want {
			close(stop)
			<-done
			t.Fatalf("hash changed or failed during churn: %q, want %q", got, want)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
