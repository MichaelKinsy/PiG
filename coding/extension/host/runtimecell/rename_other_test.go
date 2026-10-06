//go:build !windows

package runtimecell

import (
	"syscall"
	"testing"
)

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
