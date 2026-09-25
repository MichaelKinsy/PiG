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
// We intentionally do NOT call os.Exit from the signal handler: we
// re-raise the signal after cleanup so callers (make, shells, CI) see
// the correct exit status (128+signum).
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
		_ = syscall.Kill(syscall.Getpid(), sig.(syscall.Signal))
	}()

	code := m.Run()
	cleanupAllSessions()
	os.Exit(code)
}
