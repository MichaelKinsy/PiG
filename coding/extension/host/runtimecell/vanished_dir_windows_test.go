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
	if !directoryVanished(pending, exists, windowsDeletion) {
		t.Error("ERROR_DELETE_PENDING was not taken for a vanished directory")
	}
	if !directoryVanished(denied, func() error { return gone }, windowsDeletion) {
		t.Error("ERROR_ACCESS_DENIED on a directory that is gone was not taken for a vanished directory")
	}
	if !directoryVanished(denied, func() error { return denied }, windowsDeletion) {
		t.Error("ERROR_ACCESS_DENIED on a directory still pending deletion was not taken for a vanished directory")
	}
	if directoryVanished(denied, exists, windowsDeletion) {
		t.Error("ERROR_ACCESS_DENIED on a directory that is there was taken for a vanished directory")
	}
	if directoryVanished(errors.New("other"), func() error { return gone }, windowsDeletion) {
		t.Error("an unrelated error was taken for a vanished directory")
	}
}
