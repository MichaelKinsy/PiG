//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// MoveFileExW does not copy user-settable Win32 file attributes (such as HIDDEN or NOT_CONTENT_INDEXED on a dotfile-like config) from the replaced
// file onto the replacement. carryFileAttributes copies them onto the temporary file before the move.
func TestWindowsWriteFilePreservesHiddenAndUserAttributes(t *testing.T) {
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
	if err := windows.SetFileAttributes(ptr, before|wantMask); err != nil {
		t.Fatal(err)
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
	if got := readString(t, path); got != "new" {
		t.Fatalf("file = %q, want new", got)
	}
}
