package extensionconformance

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream types.ts:1315-1338 (isBashToolResult ... isLsToolResult): every runtime's guards tell the tool that ran from the tool_result event's toolName.
// The Session emits CustomToolResultEvent for every tool (coding/session_extension_hooks.go), so the event here is the production one, not a typed
// per-tool variant. Each fixture's handler reports the guards that hold for the event it received; the expected value is distinct from every SDK's
// fallback (an SDK that never runs its guard reports an empty list).
func TestToolResultGuardsMatchAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	tools := []string{"bash", "read", "ls", "my-bash"}
	want := []string{"tool-result-guards=bash:info", "tool-result-guards=read:info", "tool-result-guards=ls:info", "tool-result-guards=:info"}
	for _, test := range allHarnessCases() {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			for _, tool := range tools {
				event := extension.CustomToolResultEvent{
					ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: tool + "-guard", Input: map[string]any{}, Content: []any{map[string]any{"type": "text", "text": "output"}}},
					ToolName:            tool,
				}
				if _, err := h.runner.EmitToolResult(ctx, event); err != nil {
					t.Fatalf("%s tool_result: %v", tool, err)
				}
			}
			guards := func() []string {
				var recorded []string
				for _, event := range h.ui.Recorded() {
					if strings.HasPrefix(event, "tool-result-guards=") {
						recorded = append(recorded, event)
					}
				}
				return recorded
			}
			waitFor(t, func() bool { return len(guards()) >= len(want) })
			if got := guards(); !reflect.DeepEqual(got, want) {
				t.Fatalf("recorded guard reports = %#v, want %#v", got, want)
			}
		})
	}
}
