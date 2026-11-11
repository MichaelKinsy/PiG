package inproc

import (
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pig additive (D214): the runner owns thinking runs while any of its extensions does, and reports each change once.
func TestRunnerOwnsThinkingRunsAcrossExtensions(t *testing.T) {
	a, b := extension.Extension{Name: "a"}, extension.Extension{Name: "b"}
	a.InitializeThinkingRuns()
	b.InitializeThinkingRuns()
	runner := NewRunner([]extension.Extension{a, b}, t.TempDir())
	var calls atomic.Int32
	stop := runner.ObserveThinkingRuns(func() { calls.Add(1) })
	defer stop()
	if runner.OwnsThinkingRuns() {
		t.Fatal("no extension owns thinking runs")
	}
	a.SetOwnsThinkingRuns(true)
	b.SetOwnsThinkingRuns(true)
	if !runner.OwnsThinkingRuns() || calls.Load() != 2 {
		t.Fatalf("owns=%v observed=%d", runner.OwnsThinkingRuns(), calls.Load())
	}
	a.SetOwnsThinkingRuns(false)
	if !runner.OwnsThinkingRuns() {
		t.Fatal("the second extension still owns thinking runs")
	}
	b.SetOwnsThinkingRuns(false)
	if runner.OwnsThinkingRuns() || calls.Load() != 4 {
		t.Fatalf("owns=%v observed=%d", runner.OwnsThinkingRuns(), calls.Load())
	}
	stop()
	a.SetOwnsThinkingRuns(true)
	if calls.Load() != 4 {
		t.Fatalf("a stopped observer was called: %d", calls.Load())
	}
}
