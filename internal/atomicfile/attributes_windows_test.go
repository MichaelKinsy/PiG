//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// MoveFileExW does not copy user-settable Win32 file attributes (such as HIDDEN or NOT_CONTENT_INDEXED on a dotfile-like config) from the replaced
// file onto the replacement. carryFileAttributes copies HIDDEN, SYSTEM and NOT_CONTENT_INDEXED onto the temporary file before the move, while leaving
// FILE_ATTRIBUTE_ARCHIVE set on the newly written file even when the old file had ARCHIVE cleared (matching an in-place write so backup tools see the change).
func TestWindowsWriteFilePreservesHiddenAndUserAttributesAndSetsArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	const wantMask = windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED
	before, err := windows.GetFileAttributes(ptr)
	if err != nil {
		t.Fatal(err)
	}
	// Set HIDDEN + NOT_CONTENT_INDEXED and explicitly CLEAR ARCHIVE on the old file.
	cleared := (before &^ windows.FILE_ATTRIBUTE_ARCHIVE) | wantMask
	if err := windows.SetFileAttributes(ptr, cleared); err != nil {
		t.Fatal(err)
	}
	checked, err := windows.GetFileAttributes(ptr)
	if err != nil {
		t.Fatal(err)
	}
	if checked&windows.FILE_ATTRIBUTE_ARCHIVE != 0 {
		t.Fatalf("setup failed: ARCHIVE bit still set (0x%x)", checked)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile = %v", err)
	}
	after, err := windows.GetFileAttributes(ptr)
	if err != nil {
		t.Fatal(err)
	}
	if after&wantMask != wantMask {
		t.Fatalf("attributes after WriteFile = 0x%x, want HIDDEN|NOT_CONTENT_INDEXED (0x%x) preserved", after, wantMask)
	}
	if after&windows.FILE_ATTRIBUTE_ARCHIVE == 0 {
		t.Fatalf("attributes after WriteFile = 0x%x, want FILE_ATTRIBUTE_ARCHIVE set on rewritten file", after)
	}
	if got := readString(t, path); got != "new" {
		t.Fatalf("file = %q, want new", got)
	}
}
