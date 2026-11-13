//go:build !race

package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The agent loop asks HasHandlers before every event it might dispatch, so the question must cost no allocation and no lock: it ran a lock pair and a handler-slice copy per extension per event.
func TestHasHandlersAllocatesNothing(t *testing.T) {
	exts := make([]extension.Extension, 6)
	for i := range exts {
		exts[i].AddEventHandler("agent_end", 1, func(...any) (any, error) { return nil, nil })
	}
	exts[5].AddJSONEventHandler("message_update", 2, func(...any) (any, error) { return nil, nil })
	runner := inproc.NewRunner(exts, ".")
	if !runner.HasHandlers("agent_end") || !runner.HasHandlers("message_update") || runner.HasHandlers("turn_start") {
		t.Fatal("HasHandlers misreports the registered events")
	}
	allocs := testing.AllocsPerRun(200, func() {
		runner.HasHandlers("agent_end")
		runner.HasHandlers("message_update")
		runner.HasHandlers("turn_start")
	})
	if allocs != 0 {
		t.Fatalf("HasHandlers allocated %.1f times per three calls, want 0", allocs)
	}
}
