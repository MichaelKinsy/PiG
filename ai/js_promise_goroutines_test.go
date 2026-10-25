//go:build !race

package ai

import (
	"runtime"
	"testing"
)

// A generator that never receives a request holds no goroutine, and disposing it is a no-op.
//
// The goroutine count is exact only where a coroutine's goroutine is gone when its stop returns, as iter.Pull's is. Under the race detector `pull` is a goroutine pair (pull_race.go: the Go
// runtime never ends a coroutine's race context, so iter.Pull leaks the detector's state for every coroutine), whose goroutine is still unwinding for a moment after stop, so the builds
// without -race enforce this.
func TestJSAsyncGeneratorUnstartedHoldsNoCoroutine(t *testing.T) {
	baseline := runtime.NumGoroutine()
	executor := &continuationExecutor{}
	executor.run(func(*continuationTurn) {
		for range 100 {
			newJSAsyncGenerator(executor, func(*jsGeneratorBody[int]) (int, error) { return 0, nil }).dispose()
		}
	})
	if got := runtime.NumGoroutine(); got > baseline {
		t.Fatalf("goroutines = %d, want at most %d", got, baseline)
	}
}
