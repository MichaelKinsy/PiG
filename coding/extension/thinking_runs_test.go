package extension_test

import (
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pig additive (D214): the extension's ownership of thinking runs is one flag per extension, shared by every copy of the
// Extension, whose changes (and only changes) reach an observer.
func TestExtensionOwnsThinkingRuns(t *testing.T) {
	var ext extension.Extension
	ext.InitializeThinkingRuns()
	copied := ext // the runner holds a copy of the host's Extension
	var calls atomic.Int32
	stop := copied.ObserveThinkingRuns(func() { calls.Add(1) })
	if ext.OwnsThinkingRuns() || copied.OwnsThinkingRuns() {
		t.Fatal("a new extension owns thinking runs")
	}
	ext.SetOwnsThinkingRuns(true)
	if !copied.OwnsThinkingRuns() || !ext.OwnsThinkingRuns() || calls.Load() != 1 {
		t.Fatalf("after owning: ext=%v copy=%v observed=%d", ext.OwnsThinkingRuns(), copied.OwnsThinkingRuns(), calls.Load())
	}
	ext.SetOwnsThinkingRuns(true) // no change
	if calls.Load() != 1 {
		t.Fatalf("an unchanged flag was reported: %d", calls.Load())
	}
	ext.SetOwnsThinkingRuns(false)
	if copied.OwnsThinkingRuns() || calls.Load() != 2 {
		t.Fatalf("after withdrawing: copy=%v observed=%d", copied.OwnsThinkingRuns(), calls.Load())
	}
	stop()
	stop() // idempotent
	ext.SetOwnsThinkingRuns(true)
	if calls.Load() != 2 {
		t.Fatalf("a stopped observer was called: %d", calls.Load())
	}
}
