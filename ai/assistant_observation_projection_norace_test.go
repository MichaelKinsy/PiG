//go:build !race

package ai

import "testing"

// projectionScalars is where the test stores what a renderer keeps of a projection: a heap location, as a block's state is.
var projectionScalars struct {
	stopReason   StopReason
	errorMessage string
}

// A projection appends to the caller's slice and copies no block: into a buffer on the caller's stack it allocates nothing, whatever the arguments hold, even when the caller keeps the stop reason and error message in a heap object (updateAssistantMessageBlock).
func TestObserveProjectionAllocatesNothing(t *testing.T) {
	arguments := JsonObject{}
	for _, key := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		arguments[key] = map[string]any{"nested": []any{key, float64(1)}}
	}
	message := &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "boom", Content: []AssistantContentBlock{TextContent{Text: "t"}, ThinkingContent{Thinking: "r"}, ToolCall{ID: "c", Name: "n", Arguments: arguments}}}
	cell := newAssistantMessageCell(message)
	for name, view := range map[string]*AssistantMessage{"plain": message, "full": cell.view(), "shallow": cell.view().ShallowCopy()} {
		blocks := 0
		allocs := testing.AllocsPerRun(100, func() {
			var scratch [4]AssistantContentProjection
			content, stopReason, errorMessage := view.ObserveProjection(scratch[:0])
			blocks = len(content)
			projectionScalars.stopReason, projectionScalars.errorMessage = stopReason, errorMessage
		})
		if allocs != 0 || blocks != 3 {
			t.Errorf("%s: %v allocations for %d blocks, want 0 for 3", name, allocs, blocks)
		}
	}
}
