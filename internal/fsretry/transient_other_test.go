//go:build !windows

package fsretry

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Unix renames are not subject to the Windows sharing model, so no error is
// retried: EACCES and EPERM there are real permission failures.
func TestTransientRenameNeverRetriesOnUnix(t *testing.T) {
	for _, err := range []error{
		syscall.EACCES, syscall.EPERM, syscall.EBUSY, syscall.ENOENT,
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EACCES},
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EBUSY},
	} {
		if TransientRename(err) {
			t.Errorf("TransientRename(%v) = true on Unix", err)
		}
	}
}

func TestRenameDoesNotRetryOnUnix(t *testing.T) {
	dir := t.TempDir()
	waited := false
	err := Rename(t.Context(), filepath.Join(dir, "missing"), filepath.Join(dir, "moved"), func(context.Context, time.Duration) error {
		waited = true
		return nil
	})
	if !os.IsNotExist(err) || waited {
		t.Fatalf("Rename = %v (waited %v), want the rename error without a wait", err, waited)
	}
}
