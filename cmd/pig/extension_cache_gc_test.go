package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// beyondCacheRetention moves a fake clock past the 30-day retention of an unused entry.
const beyondCacheRetention = 90 * 24 * time.Hour

// isolateAutomaticCacheGC gives a test its own agent directory, an empty schedule, and the production
// collector and hooks, which the cleanup restores.
func isolateAutomaticCacheGC(t *testing.T) (cacheRoot string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PI_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	run, prune, now, rename, removeAll := automaticCacheGCRun, automaticCacheGCPrune, automaticCacheGCNow, automaticCacheGCRename, automaticCacheGCRemoveAll
	t.Cleanup(func() {
		stopAutomaticExtensionCacheGC()
		resetAutomaticExtensionCacheGC()
		automaticCacheGCRun, automaticCacheGCPrune, automaticCacheGCNow, automaticCacheGCRename, automaticCacheGCRemoveAll = run, prune, now, rename, removeAll
	})
	resetAutomaticExtensionCacheGC()
	return filepath.Join(home, "cache")
}

// publishExpiredCacheEntry publishes a cache entry and returns a clock far enough ahead that retention has
// expired it.
func publishExpiredCacheEntry(t *testing.T, cacheRoot string) (entry string, later func() time.Time) {
	t.Helper()
	dir := filepath.Join(cacheRoot, "cells", "go", "deadbeef")
	published, err := runtimecell.PublishArtifact(context.Background(), dir, "runner", "deadbeef", "go", func(scratch string) (string, error) {
		artifact := filepath.Join(scratch, "runner")
		return artifact, os.WriteFile(artifact, make([]byte, 1024), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(beyondCacheRetention)
	return published.Dir, func() time.Time { return future }
}

// Startup never waits for the cache collection. The collector is in the middle of removing an entry,
// blocked in the file system, and start has returned: the daily collection of a 1.4 GB cache on NFS took
// tens of minutes while pig accepted no input.
func TestStartAutomaticExtensionCacheGCReturnsBeforeCollectionFinishes(t *testing.T) {
	cacheRoot := isolateAutomaticCacheGC(t)
	_, later := publishExpiredCacheEntry(t, cacheRoot)
	automaticCacheGCNow = later
	removing := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCollection := func() { releaseOnce.Do(func() { close(release) }) }
	// A start that waits for the collection holds it until the test ends; releasing it lets the test fail instead of hanging.
	t.Cleanup(releaseCollection)
	automaticCacheGCRemoveAll = func(string) error {
		close(removing)
		<-release
		return nil
	}

	scheduleAutomaticExtensionCacheGC(nil)
	returned := make(chan struct{})
	go func() {
		startAutomaticExtensionCacheGC(t.Context())
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("startAutomaticExtensionCacheGC waited for the collection")
	}
	select {
	case <-removing:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the collection never started")
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); !os.IsNotExist(err) {
		t.Fatalf("marker written before the collection finished: %v", err)
	}
	releaseCollection()
	// Wait for the collection to finish on its own: stopping it first would cancel it, and a cancelled collection writes no marker.
	extensionCacheGC.mu.Lock()
	done := extensionCacheGC.done
	extensionCacheGC.mu.Unlock()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the released collection did not finish")
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); err != nil {
		t.Fatalf("finished collection wrote no marker: %v", err)
	}
}

func TestStartAutomaticExtensionCacheGCRunsOnlyWhenScheduledAndOnlyOnce(t *testing.T) {
	isolateAutomaticCacheGC(t)
	var runs atomic.Int32
	automaticCacheGCRun = func(context.Context, []subprocess.ExtConfig) error { runs.Add(1); return nil }

	startAutomaticExtensionCacheGC(t.Context())
	stopAutomaticExtensionCacheGC()
	if runs.Load() != 0 {
		t.Fatalf("collection ran with nothing scheduled: %d", runs.Load())
	}
	scheduleAutomaticExtensionCacheGC([]subprocess.ExtConfig{{Name: "a"}})
	startAutomaticExtensionCacheGC(t.Context())
	startAutomaticExtensionCacheGC(t.Context())
	stopAutomaticExtensionCacheGC()
	if runs.Load() != 1 {
		t.Fatalf("collections = %d, want one per process", runs.Load())
	}
}

// The collection is bound to the process: stopping cancels it and waits for it, and the collector sees the cancellation.
func TestStopAutomaticExtensionCacheGCCancelsTheCollection(t *testing.T) {
	isolateAutomaticCacheGC(t)
	running := make(chan struct{})
	var observed atomic.Value
	automaticCacheGCRun = func(ctx context.Context, _ []subprocess.ExtConfig) error {
		close(running)
		<-ctx.Done()
		observed.Store(ctx.Err())
		return ctx.Err()
	}
	scheduleAutomaticExtensionCacheGC(nil)
	startAutomaticExtensionCacheGC(t.Context())
	<-running
	stopped := make(chan struct{})
	go func() { stopAutomaticExtensionCacheGC(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("stop did not return")
	}
	if err, _ := observed.Load().(error); !errors.Is(err, context.Canceled) {
		t.Fatalf("collector context error = %v, want context.Canceled", err)
	}
	if message := readAutomaticCacheGCFailure(); message != "" {
		t.Fatalf("a cancelled collection recorded a failure: %q", message)
	}
}

// A collection that fails in the background cannot write to the terminal the interactive UI owns. It records
// the failure for `pig extensions cache stats`, and a later success clears it.
func TestAutomaticExtensionCacheGCRecordsFailureInsteadOfPrinting(t *testing.T) {
	isolateAutomaticCacheGC(t)
	automaticCacheGCRun = func(context.Context, []subprocess.ExtConfig) error { return errors.New("disk on fire") }
	scheduleAutomaticExtensionCacheGC(nil)
	startAutomaticExtensionCacheGC(t.Context())
	stopAutomaticExtensionCacheGC()
	if got := readAutomaticCacheGCFailure(); got != "disk on fire" {
		t.Fatalf("recorded failure = %q", got)
	}

	resetAutomaticExtensionCacheGC()
	automaticCacheGCRun = func(context.Context, []subprocess.ExtConfig) error { return nil }
	scheduleAutomaticExtensionCacheGC(nil)
	startAutomaticExtensionCacheGC(t.Context())
	stopAutomaticExtensionCacheGC()
	if got := readAutomaticCacheGCFailure(); got != "" {
		t.Fatalf("failure survived a successful collection: %q", got)
	}
}

// The collection that found an entry busy still finishes: it records its marker, so the next start does not
// repeat it. NFS renames a file open elsewhere to `.nfs*` and answers EBUSY when the collection removes it;
// the old code returned that as an error, never wrote the marker, and collected again on every start.
func TestAutomaticExtensionCacheGCWritesMarkerWhenAnEntryIsBusy(t *testing.T) {
	cacheRoot := isolateAutomaticCacheGC(t)
	_, later := publishExpiredCacheEntry(t, cacheRoot)
	automaticCacheGCNow = later
	automaticCacheGCRemoveAll = func(path string) error {
		return &os.PathError{Op: "unlink", Path: filepath.Join(path, ".nfs0000000000000042"), Err: syscall.EBUSY}
	}
	if err := runAutomaticExtensionCacheGC(t.Context(), nil); err != nil {
		t.Fatalf("collection with a busy entry failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); err != nil {
		t.Fatalf("no marker after a collection that skipped a busy entry: %v", err)
	}
}

// A real failure still leaves the marker unwritten, so the next start collects again.
func TestAutomaticExtensionCacheGCWritesNoMarkerOnRealFailure(t *testing.T) {
	cacheRoot := isolateAutomaticCacheGC(t)
	_, later := publishExpiredCacheEntry(t, cacheRoot)
	automaticCacheGCNow = later
	automaticCacheGCRemoveAll = func(string) error { return syscall.EACCES }
	if err := runAutomaticExtensionCacheGC(t.Context(), nil); err == nil {
		t.Fatal("collection with a permission failure succeeded")
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); !os.IsNotExist(err) {
		t.Fatalf("marker written after a failed collection: %v", err)
	}
}

// The marker suppresses the next collection for a day, measured by the collector's clock.
func TestAutomaticExtensionCacheGCMarkerSuppressesTheNextRunWithinTheInterval(t *testing.T) {
	cacheRoot := isolateAutomaticCacheGC(t)
	var prunes atomic.Int32
	automaticCacheGCPrune = func(options runtimecell.CacheLifecycleOptions) (runtimecell.CacheReport, error) {
		prunes.Add(1)
		return runtimecell.PruneCaches(options)
	}
	written := time.Now()
	automaticCacheGCNow = func() time.Time { return written }
	if err := runAutomaticExtensionCacheGC(t.Context(), nil); err != nil || prunes.Load() != 1 {
		t.Fatalf("first run: prunes=%d err=%v", prunes.Load(), err)
	}
	info, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc"))
	if err != nil {
		t.Fatal(err)
	}
	markerTime := info.ModTime()

	automaticCacheGCNow = func() time.Time { return markerTime.Add(23 * time.Hour) }
	if err := runAutomaticExtensionCacheGC(t.Context(), nil); err != nil || prunes.Load() != 1 {
		t.Fatalf("run inside the interval: prunes=%d err=%v", prunes.Load(), err)
	}
	automaticCacheGCNow = func() time.Time { return markerTime.Add(25 * time.Hour) }
	if err := runAutomaticExtensionCacheGC(t.Context(), nil); err != nil || prunes.Load() != 2 {
		t.Fatalf("run after the interval: prunes=%d err=%v", prunes.Load(), err)
	}
}

// A cancelled collection records no marker, so it is not mistaken for a finished one.
func TestAutomaticExtensionCacheGCCancelledWritesNoMarker(t *testing.T) {
	cacheRoot := isolateAutomaticCacheGC(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runAutomaticExtensionCacheGC(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); !os.IsNotExist(err) {
		t.Fatalf("marker written by a cancelled collection: %v", err)
	}
}

// A collection cancelled while it removes its last entry writes no marker, even when the file system then reports
// that entry busy: a busy entry is skipped, but the run did not finish.
func TestAutomaticExtensionCacheGCCancelledAtTheLastEntryWritesNoMarker(t *testing.T) {
	cacheRoot := isolateAutomaticCacheGC(t)
	_, later := publishExpiredCacheEntry(t, cacheRoot)
	automaticCacheGCNow = later
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	automaticCacheGCRename = func(source, _ string) error {
		cancel()
		return &os.LinkError{Op: "rename", Old: source, New: source + ".tombstone", Err: syscall.EBUSY}
	}
	if err := runAutomaticExtensionCacheGC(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); !os.IsNotExist(err) {
		t.Fatalf("marker written by a cancelled collection: %v", err)
	}
}

// Loading extensions, the startup path, only schedules the collection: nothing collects until a mode
// starts it, so a load never waits for the cache.
func TestLoadingExtensionsSchedulesButDoesNotRunTheCacheCollection(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	cacheRoot := isolateAutomaticCacheGC(t)
	var runs, prunes atomic.Int32
	automaticCacheGCRun = func(context.Context, []subprocess.ExtConfig) error { runs.Add(1); return nil }
	// Counting prunes too catches a load that calls the collector directly rather than through the scheduled run.
	automaticCacheGCPrune = func(runtimecell.CacheLifecycleOptions) (runtimecell.CacheReport, error) {
		prunes.Add(1)
		return runtimecell.CacheReport{}, nil
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	configs := []subprocess.ExtConfig{{Name: "ctx-mode", Source: fixture, Enabled: true}}
	_, host, _, errs := loadSubprocessExtensions(t.Context(), t.TempDir(), extension.ModePrint, nil, configs, nil, nil)
	if len(errs) != 0 {
		t.Fatalf("load errors = %v", errs)
	}
	defer host.Shutdown("test done")
	if runs.Load() != 0 || prunes.Load() != 0 {
		t.Fatalf("loading extensions ran the cache collection: runs=%d prunes=%d", runs.Load(), prunes.Load())
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, ".last-auto-gc")); !os.IsNotExist(err) {
		t.Fatalf("loading extensions wrote the collection marker: %v", err)
	}
	startAutomaticExtensionCacheGC(t.Context())
	stopAutomaticExtensionCacheGC()
	if runs.Load() != 1 {
		t.Fatalf("started collections = %d, want 1 after the load scheduled one", runs.Load())
	}
}

// `pig extensions cache stats` shows the failure of the last background collection, which could not print it.
func TestExtensionsCacheStatsShowsTheLastAutomaticPruneFailure(t *testing.T) {
	cacheRoot := isolateAutomaticCacheGC(t)
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, ".auto-gc.error"), []byte("prune extension cache: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runExtensionsCommand([]string{"extensions", "cache", "stats"})
	})
	if code != 0 || !strings.Contains(stderr, "the last automatic prune failed: prune extension cache: boom") {
		t.Fatalf("stats code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
