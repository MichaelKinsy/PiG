package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// abortAfterChecks is a Context whose Err reports cancellation from its (after+1)th call on, so a test can abort an
// operation between two of its abort checks. Done never closes.
type abortAfterChecks struct {
	context.Context
	after int32
	calls atomic.Int32
}

func (ctx *abortAfterChecks) Err() error {
	if ctx.calls.Add(1) > ctx.after {
		return context.Canceled
	}
	return nil
}

func wantAbortedFileError(t *testing.T, err error) {
	t.Helper()
	fileError, ok := errors.AsType[*durableenv.FileError](err)
	if !ok || fileError.Code != durableenv.FileErrorAborted {
		t.Fatalf("error = %v, want the aborted FileError", err)
	}
}

// Pi packages/durable/src/env/node.ts:624-625: watch checks the signal before it opens anything. A watch that would
// fail to open (no directory may be watched) therefore reports aborted, not the open failure.
// mutation-checked: dropping the check before the open fails it.
func TestWatchWithAnAbortedContextFailsBeforeOpening(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: NodeWatchOptions{MaxDirectories: new(0)}})
	ctx, cancel := context.WithCancel(background)
	cancel()
	watcher, err := env.Watch(ctx, []durableenv.WatchTarget{{Path: "."}}, func(durableenv.WatchChange) {})
	if watcher != nil {
		t.Fatal("an aborted watch returned a watcher")
	}
	wantAbortedFileError(t, err)
}

// Pi packages/durable/src/env/node.ts:632-636: a signal aborted while the watcher opens closes the opened watcher and
// reports aborted, so no change is ever delivered.
// mutation-checked: dropping the check after the open fails it, and so does dropping the close of the opened watcher.
func TestWatchAbortedWhileOpeningClosesTheWatcherAndFails(t *testing.T) {
	cwd := t.TempDir()
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: cwd, Watch: NodeWatchOptions{Mode: durableenv.WatchPolling, PollIntervalMs: new(5)}})
	var delivered atomic.Int32
	watcher, err := env.Watch(&abortAfterChecks{Context: background, after: 1}, []durableenv.WatchTarget{{Path: ".", Recursive: true}}, func(durableenv.WatchChange) { delivered.Add(1) })
	if watcher != nil {
		t.Fatal("a watch aborted while opening returned a watcher")
	}
	wantAbortedFileError(t, err)
	if err := os.WriteFile(filepath.Join(cwd, "after.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Sixty poll intervals: an open polling watcher reports the new file within the first few.
	time.Sleep(300 * time.Millisecond)
	if got := delivered.Load(); got != 0 {
		t.Fatalf("%d change(s) were delivered by the watcher of an aborted watch", got)
	}
}
