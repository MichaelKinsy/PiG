package tui

import (
	"os"
	"syscall"
	"testing"
)

func TestUpstreamRefreshTerminalDimensions(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/regression-sigwinch-kill-eacces.test.ts:9
	t.Run("does not throw when kill(2) returns EACCES for self-signal", func(t *testing.T) {
		pid := os.Getpid()
		called := false
		refreshTerminalDimensions(false, pid, func(target int) error {
			called = true
			if target != pid {
				t.Fatalf("target pid = %d, want self %d", target, pid)
			}
			return syscall.EACCES
		})
		if !called {
			t.Fatal("self-signal was not attempted")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-sigwinch-kill-eacces.test.ts:27
	t.Run("does not call kill on win32", func(t *testing.T) {
		called := false
		refreshTerminalDimensions(true, os.Getpid(), func(int) error { called = true; return nil })
		if called {
			t.Fatal("kill should not be called on win32")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/regression-sigwinch-kill-eacces.test.ts:41
	t.Run("preserves other error codes", func(t *testing.T) {
		// The upstream name is misleading: its assertion ignores EPERM too, because the refresh is best-effort.
		called := false
		refreshTerminalDimensions(false, os.Getpid(), func(int) error { called = true; return syscall.EPERM })
		if !called {
			t.Fatal("self-signal was not attempted")
		}
	})
}
