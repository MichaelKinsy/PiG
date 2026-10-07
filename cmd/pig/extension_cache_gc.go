package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The daily collection of the shared extension cache runs in the background, after a mode has taken over the
// terminal or the protocol streams and while it accepts input. Loading extensions only schedules it. A
// collection of a large cache on a network file system takes minutes, and the first version ran before pig
// accepted input.
//
// pig additive (D20): the packed-runtime cache has no upstream equivalent; Pi has no cache collection at startup.

// automaticCacheGCStopWait bounds how long process exit waits for a cancelled collection to unwind.
// pig additive (D20): the packed-runtime cache has no upstream equivalent.
const automaticCacheGCStopWait = 2 * time.Second

// Test seams: the collector, its clock, and the file system operations it removes entries with.
var (
	automaticCacheGCRun       = runAutomaticExtensionCacheGC
	automaticCacheGCPrune     = runtimecell.PruneCaches
	automaticCacheGCNow       = time.Now
	automaticCacheGCRename    func(string, string) error
	automaticCacheGCRemoveAll func(string) error
)

// extensionCacheGCTask owns the background collection: scheduled by extension loads, started once by a mode,
// cancelled and drained at exit.
type extensionCacheGCTask struct {
	mu        sync.Mutex
	scheduled bool
	configs   []subprocess.ExtConfig
	started   bool
	cancel    context.CancelFunc
	done      chan struct{}
}

var extensionCacheGC extensionCacheGCTask

// scheduleAutomaticExtensionCacheGC records the extension set the collection protects. The latest load wins,
// including a /reload before the collection starts. It never touches the cache.
func scheduleAutomaticExtensionCacheGC(configs []subprocess.ExtConfig) {
	extensionCacheGC.mu.Lock()
	defer extensionCacheGC.mu.Unlock()
	extensionCacheGC.scheduled = true
	extensionCacheGC.configs = append([]subprocess.ExtConfig(nil), configs...)
}

// startAutomaticExtensionCacheGC starts the scheduled collection in the background and returns at once. It
// starts nothing when no extension load scheduled one, and a process starts at most one. The collection runs
// until it finishes or ctx is cancelled or stopAutomaticExtensionCacheGC is called.
func startAutomaticExtensionCacheGC(ctx context.Context) {
	extensionCacheGC.mu.Lock()
	defer extensionCacheGC.mu.Unlock()
	if !extensionCacheGC.scheduled || extensionCacheGC.started {
		return
	}
	extensionCacheGC.started = true
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	extensionCacheGC.cancel, extensionCacheGC.done = cancel, done
	configs, run := extensionCacheGC.configs, automaticCacheGCRun
	go func() {
		defer close(done)
		defer cancel()
		recordAutomaticCacheGCResult(runCtx, run(runCtx, configs))
	}()
}

// stopAutomaticExtensionCacheGC cancels the collection and waits for it to unwind, for a bounded time, so a
// file system call that does not return cannot hold the process open. An unfinished collection leaves
// tombstones the next collection removes.
func stopAutomaticExtensionCacheGC() {
	extensionCacheGC.mu.Lock()
	cancel, done := extensionCacheGC.cancel, extensionCacheGC.done
	extensionCacheGC.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(automaticCacheGCStopWait):
	}
}

func resetAutomaticExtensionCacheGC() {
	extensionCacheGC.mu.Lock()
	defer extensionCacheGC.mu.Unlock()
	extensionCacheGC.scheduled, extensionCacheGC.configs = false, nil
	extensionCacheGC.started, extensionCacheGC.cancel, extensionCacheGC.done = false, nil, nil
}

func automaticCacheGCFailurePath() string {
	return filepath.Join(codingagent.ConfigRoot(), "cache", ".auto-gc.error")
}

// recordAutomaticCacheGCResult keeps a failure where `pig extensions cache stats` shows it. The collection
// runs while a terminal UI owns the screen, so it never prints. A cancelled collection records nothing.
func recordAutomaticCacheGCResult(ctx context.Context, err error) {
	path := automaticCacheGCFailurePath()
	switch {
	case err == nil:
		_ = os.Remove(path)
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
	default:
		if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
			// pig additive (D20): the status file is best effort; the failure has no other place to go while a UI owns the screen.
			_ = os.WriteFile(path, []byte(err.Error()+"\n"), 0o644)
		}
	}
}

// readAutomaticCacheGCFailure returns the last background collection's failure, or "".
func readAutomaticCacheGCFailure() string {
	data, err := os.ReadFile(automaticCacheGCFailurePath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
