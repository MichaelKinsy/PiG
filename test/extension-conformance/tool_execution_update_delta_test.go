package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// toolUpdateStream is the partial results of parallel tool calls that interleave their updates: texts that grow past the length from which the host sends only the appended text, a result whose details change, a block that is replaced, and more calls than the host keeps a payload for, so the runtimes evict and receive keyframes again.
func toolUpdateStream() []extension.ToolExecutionUpdateEvent {
	const calls = 12
	texts := make([]string, calls)
	var events []extension.ToolExecutionUpdateEvent
	for round := range 6 {
		for call := range calls {
			if call%3 == 2 && round%2 == 1 {
				continue
			}
			texts[call] += fmt.Sprintf("call %d round %d writes a line of output äöü that is longer than the append framing;\n", call, round)
			blocks := []any{map[string]any{"type": "text", "text": texts[call]}}
			if call == 1 && round == 4 {
				blocks = []any{map[string]any{"type": "text", "text": "replaced"}, map[string]any{"type": "text", "text": texts[call]}}
			}
			partial := map[string]any{"content": blocks, "details": map[string]any{"progress": round, "call": call}}
			events = append(events, extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: fmt.Sprintf("call-%d", call), ToolName: "make", Args: map[string]any{"command": "make", "target": call}, PartialResult: partial})
		}
	}
	return events
}

// TestConformance_ToolExecutionUpdateDeltas pins that an extension process receives, for every tool_execution_update, the object the whole payload would have been, whatever the interleaving of the tool calls. The host sends the first update of a call whole and each later one as the operations that turn the call's previous payload into it (EventStream, coding/extension/host/subprocess/protocol.go); each SDK keeps a payload per call and applies them. A runtime that dropped a delta, applied one to another call's payload, kept a stale copy or lost an evicted one reports a payload that differs from the one the event encodes to.
func TestConformance_ToolExecutionUpdateDeltas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	events := toolUpdateStream()
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx := context.Background()
			const prefix = "event_payload:tool_execution_update:"
			var failures []string
			removeListener := h.runner.AddErrorListener(func(err *extension.ExtensionError) { failures = append(failures, err.Error) })
			defer removeListener()
			*h.notify = (*h.notify)[:0]
			runConformanceCommandArgs(t, h, "event_payload_probe", "tool_execution_update")
			for index, event := range events {
				if _, err := h.runner.Emit(ctx, event); err != nil {
					t.Fatalf("emit tool_execution_update %d: %v", index, err)
				}
			}
			var reported []string
			pollUntilConformance(t, "the handler did not report every tool_execution_update", func() bool {
				reported = reported[:0]
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, prefix); ok {
						reported = append(reported, strings.TrimSuffix(rest, ":info"))
					}
				}
				return len(reported) >= len(events)
			})
			// A runtime that cannot apply a delta answers with an error, and the host recovers with a whole payload that the next handler receives: the handler that failed, not the probe, saw the broken stream.
			if len(failures) > 0 {
				t.Fatalf("a handler failed during the stream: %v", failures)
			}
			for index, event := range events {
				whole, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err := json.Unmarshal([]byte(reported[index]), &got); err != nil {
					t.Fatalf("payload %d is not JSON: %v", index, err)
				}
				if err := json.Unmarshal(whole, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("payload %d = %s, want %s", index, reported[index], whole)
				}
			}
		})
	}
}
