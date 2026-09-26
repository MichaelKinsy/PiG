//go:build windows

package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestReadSharingDeleteCoexistsWithARenameHandle reproduces the
// TestStopAfterNegotiationOnlyInput flake deterministically. It holds the
// kind of handle MoveFileEx keeps on a file it has just renamed: DELETE
// access, sharing read and write. os.ReadFile then fails with
// ERROR_SHARING_VIOLATION, which is what the pseudo-console key reader hit
// when it read keys.N while the child's rename was finishing;
// readSharingDelete reads the file.
func TestReadSharingDeleteCoexistsWithARenameHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.1")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	renameHandle, err := windows.CreateFile(name, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(renameHandle) }()

	if _, err := os.ReadFile(path); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("os.ReadFile under a DELETE handle = %v, want ERROR_SHARING_VIOLATION (the flake)", err)
	}
	text, err := readSharingDelete(path)
	if err != nil || string(text) != "x" {
		t.Fatalf("readSharingDelete = %q, %v", text, err)
	}
	if _, err := readSharingDelete(path + ".missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readSharingDelete(missing) = %v, want os.ErrNotExist", err)
	}
}
