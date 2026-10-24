//go:build windows

package runtimecell

import (
	"errors"
	"io/fs"
	"testing"

	"golang.org/x/sys/windows"
)

// Windows opens a directory that another process is deleting with ERROR_DELETE_PENDING or ERROR_ACCESS_DENIED, never ERROR_FILE_NOT_FOUND.
func TestDirectoryVanishedOnWindowsDeletionCodes(t *testing.T) {
	pending := &fs.PathError{Op: "open", Path: "dir", Err: windows.ERROR_DELETE_PENDING}
	denied := &fs.PathError{Op: "open", Path: "dir", Err: windows.ERROR_ACCESS_DENIED}
	gone := &fs.PathError{Op: "lstat", Path: "dir", Err: windows.ERROR_FILE_NOT_FOUND}
	exists := func() error { return nil }
	if !NewVanishTracker().vanished("dir", pending, exists) {
		t.Error("ERROR_DELETE_PENDING was not taken for a vanished directory")
	}
	if !NewVanishTracker().vanished("dir", denied, func() error { return gone }) {
		t.Error("ERROR_ACCESS_DENIED on a directory that is gone was not taken for a vanished directory")
	}
	if !NewVanishTracker().vanished("dir", denied, func() error { return denied }) {
		t.Error("ERROR_ACCESS_DENIED on a directory still pending deletion was not taken for a vanished directory")
	}
	if NewVanishTracker().vanished("dir", denied, exists) {
		t.Error("ERROR_ACCESS_DENIED on a directory that is there was taken for a vanished directory")
	}
	if NewVanishTracker().vanished("dir", errors.New("other"), func() error { return gone }) {
		t.Error("an unrelated error was taken for a vanished directory")
	}
}

// A directory that denies both the open and the Lstat is skipped for now and settled at the end of the walk: it is delete-pending if it still denies, gone if it is not found, and a failure if it answers Lstat.
func TestVanishTrackerOnWindowsVerifiesDoubleAccessDenied(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "dir", Err: windows.ERROR_ACCESS_DENIED}
	for _, tc := range []struct {
		name    string
		later   error
		wantErr bool
	}{
		{"gone", &fs.PathError{Op: "lstat", Path: "dir", Err: windows.ERROR_FILE_NOT_FOUND}, false},
		{"delete pending", &fs.PathError{Op: "lstat", Path: "dir", Err: windows.ERROR_ACCESS_DENIED}, false},
		{"there", nil, true},
	} {
		calls := 0
		tracker := NewVanishTracker()
		if !tracker.vanished("dir", denied, func() error {
			calls++
			if calls == 1 {
				return denied
			}
			return tc.later
		}) {
			t.Fatalf("%s: a directory denying open and Lstat was not skipped", tc.name)
		}
		if err := tracker.Verify(); (err != nil) != tc.wantErr {
			t.Errorf("%s: Verify = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

// churnRemovalBusy reports whether err is Windows refusing to delete an entry the hash walk holds open, or one already pending deletion.
func churnRemovalBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_DELETE_PENDING)
}
