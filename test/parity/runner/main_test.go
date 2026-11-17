//go:build parity

package runner

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
// After cleanup the handler uses the platform's default signal exit status, including STATUS_CONTROL_C_EXIT on Windows.
func TestMain(m *testing.M) {
	// tmux pipe-pane starts this binary as the transcript reader (transcriptReaderCommand): copy standard input to the file the runner named and exit. A test can hold it before it reads (transcriptHoldEnv).
	if path := os.Getenv(transcriptCopyEnv); path != "" {
		holdTranscriptReader(os.Getenv(transcriptHoldEnv))
		os.Exit(copyTranscript(path, os.Stdin, os.Stderr))
	}
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == fakeHTName {
		os.Exit(runFakeHT(os.Args[1:]))
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig := <-sigs
		cleanupAllSessions()
		// End the way the signal's default handling would, so the parent
		// process observes the real cause of death.
		signal.Reset(sig)
		exitWithSignal(sig.(syscall.Signal))
	}()

	code := m.Run()
	cleanupAllSessions()
	os.Exit(code)
}
