//go:build !windows

package runtimecell

import (
	"os"
	"syscall"
	"testing"
)

// Unix renames are not subject to the Windows sharing model, so no error is
// retried: EACCES and EPERM there are real permission failures.
func TestRenameRetryableNeverRetriesOnUnix(t *testing.T) {
	for _, err := range []error{
		syscall.EACCES, syscall.EPERM, syscall.EBUSY, syscall.ENOENT,
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EACCES},
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EBUSY},
	} {
		if renameRetryable(err) {
			t.Errorf("renameRetryable(%v) = true on Unix", err)
		}
	}
}

func TestPublishRenameDefaultDoesNotRetryOnUnix(t *testing.T) {
	attempts := 0
	policy := publishRename
	policy.rename = func(string, string) error { attempts++; return syscall.EACCES }
	if _, err := policy.publish(t.Context(), "a", "b", func() bool { return false }); err == nil {
		t.Fatal("publish succeeded")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want one rename on Unix", attempts)
	}
}
