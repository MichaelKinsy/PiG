package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// Upstream's signal shutdown awaits runtimeHost.dispose() with no deadline
// before it exits, so extension cleanup that takes longer than a second still
// finishes. Pig abandoned the hook after 1 s and cancelled the root context
// under it. GUARD-06.
func TestTerminationShutdownHookRunsToCompletion(t *testing.T) {
	var finished atomic.Bool
	setTerminationShutdownHook(func() {
		time.Sleep(1500 * time.Millisecond)
		finished.Store(true)
	})
	t.Cleanup(func() { setTerminationShutdownHook(nil) })
	runTerminationShutdownHook()
	if !finished.Load() {
		t.Fatal("termination returned before the shutdown hook finished")
	}
}
