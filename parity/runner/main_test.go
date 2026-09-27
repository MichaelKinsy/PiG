//go:build parity

package runner

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

// TestMain installs a signal handler that kills every still-registered
// tmux session before the process exits. This is the structural
// cleanup path for signals that would otherwise bypass per-test defers.
//
// Coverage matrix:
//
//	clean exit (all goroutines defer)  → `defer killSession` fires (happy path)
//	SIGINT  (Ctrl-C from shell)        → handler walks registry, kills all, exits
//	SIGTERM (timeout, `make` cancel)   → same
//	SIGHUP  (terminal close)           → same
//	SIGKILL                            → unrecoverable in-process; the
//	                                     Makefile's `trap` on EXIT covers
//	                                     this case at the shell layer
//
// After cleanup, Unix re-raises the signal so the parent sees signal termination. Platforms that cannot signal themselves exit with the corresponding 128+signum status.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == fakeHTName {
		os.Exit(runFakeHT(os.Args[1:]))
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig := <-sigs
		cleanupAllSessions()
		// Re-raise the signal with the default handler so the parent
		// process observes the real cause of death.
		signal.Reset(sig.(syscall.Signal))
		self, err := os.FindProcess(os.Getpid())
		if err == nil {
			err = self.Signal(sig)
			_ = self.Release()
		}
		if err != nil {
			os.Exit(128 + int(sig.(syscall.Signal)))
		}
	}()

	code := m.Run()
	cleanupAllSessions()
	os.Exit(code)
}
