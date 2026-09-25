package codingagent

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Output guard for non-TTY contexts.
//
// In headless modes such as --print and --rpc, a stray stdout write from a
// tool or extension can corrupt text or structured protocol output. The guard redirects all
// `os.Stdout` writes to `os.Stderr`; explicit `WriteRawStdout`
// callers (print-mode final text, RPC protocol frames) bypass the
// redirect via a saved reference to the original stdout.
//
// Mirrors upstream `core/output-guard.ts:takeOverStdout` /
// `restoreStdout` / `writeRawStdout` / `flushRawStdout`. The Go
// implementation uses an os-level pipe rather than monkey-patching
// `process.stdout.write` because Go's `fmt.Println` and friends
// resolve `os.Stdout` lazily on each call: reassigning the var
// is enough to redirect stdlib emit paths.
//
// Lifecycle: callers must invoke `RestoreStdout()` (typically via
// `defer`) before exit so the goroutine that ferries pipe → stderr
// shuts down cleanly.

var (
	guardMu    sync.Mutex
	guardState *outputGuardState
)

type outputGuardState struct {
	originalStdout *os.File
	pipeWriter     *os.File
	pipeReader     *os.File
	copyDone       chan struct{}
}

// TakeOverStdout redirects subsequent `os.Stdout` writes to
// `os.Stderr`. Idempotent: a second call before `RestoreStdout`
// is a no-op. The saved original stdout is reachable via
// `WriteRawStdout` for explicit framed-output callers.
func TakeOverStdout() error {
	guardMu.Lock()
	defer guardMu.Unlock()
	if guardState != nil {
		return nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("output-guard: pipe: %w", err)
	}
	original := os.Stdout
	state := &outputGuardState{
		originalStdout: original,
		pipeWriter:     w,
		pipeReader:     r,
		copyDone:       make(chan struct{}),
	}
	go func() {
		// Verbatim copy. Output-guard does NOT strip ANSI: that's
		// upstream's contract too: the guard's job is *isolation*
		// (stdout vs stderr), not sanitization. Subagent transcript
		// rendering is responsible for any further filtering.
		_, _ = io.Copy(os.Stderr, r)
		close(state.copyDone)
	}()
	os.Stdout = w
	guardState = state
	return nil
}

// RestoreStdout undoes a `TakeOverStdout`. Closes the pipe writer
// (signalling the copy goroutine to drain + exit), waits for the
// goroutine, then restores `os.Stdout`. Safe to call without a
// matching takeover (no-op).
func RestoreStdout() {
	guardMu.Lock()
	state := guardState
	guardState = nil
	guardMu.Unlock()
	if state == nil {
		return
	}
	os.Stdout = state.originalStdout
	_ = state.pipeWriter.Close()
	<-state.copyDone
	_ = state.pipeReader.Close()
}

// IsStdoutTakenOver reports whether a takeover is currently active.
func IsStdoutTakenOver() bool {
	guardMu.Lock()
	defer guardMu.Unlock()
	return guardState != nil
}

// WriteRawStdout writes `text` to the original (pre-takeover)
// stdout, bypassing the redirect. Use for explicit framed output:
// print-mode final text, RPC JSON-RPC frames. When no takeover is
// active, this is equivalent to writing directly to `os.Stdout`.
func WriteRawStdout(text string) (int, error) {
	guardMu.Lock()
	state := guardState
	guardMu.Unlock()
	if state != nil {
		return state.originalStdout.WriteString(text)
	}
	return os.Stdout.WriteString(text)
}

// RawStdoutWriter returns an `io.Writer` that writes to the
// pre-takeover stdout. Convenient for passing into helpers like
// `printToolsForMessages` that expect a Writer interface.
func RawStdoutWriter() io.Writer {
	guardMu.Lock()
	state := guardState
	guardMu.Unlock()
	if state != nil {
		return state.originalStdout
	}
	return os.Stdout
}
