package inproc

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// withUIPrompt queues the same snapshot-based emitter as direct events in Pi.
func TestUIPromptSnapshotsLaterExtensionsBeforeCallbacks(t *testing.T) {
	var calls []string
	first := extension.Extension{Path: "first"}
	first.InitializeEventHandlers()
	second := extension.Extension{Path: "second"}
	second.InitializeEventHandlers()
	first.AddEventHandler("ui_prompt_start", 1, func(...any) (any, error) {
		calls = append(calls, "A")
		second.RemoveEventHandler("ui_prompt_start", 2)
		second.AddEventHandler("ui_prompt_start", 3, func(...any) (any, error) { calls = append(calls, "C"); return nil, nil })
		return nil, nil
	})
	second.AddEventHandler("ui_prompt_start", 2, func(...any) (any, error) { calls = append(calls, "B"); return nil, nil })
	done := make(chan struct{})
	second.AddEventHandler("ui_prompt_end", 4, func(...any) (any, error) { close(done); return nil, nil })
	runner := NewRunner([]extension.Extension{first, second}, t.TempDir())
	runner.BeginUIPrompt(extension.UIPromptKindSelect, "Choose")()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	if !slices.Equal(calls, []string{"A", "B"}) {
		t.Fatalf("prompt calls=%v, want [A B]", calls)
	}
}
