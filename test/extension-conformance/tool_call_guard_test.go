package extensionconformance

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_ToolCallGuardAgreesAcrossSDKs: Pi's isToolCallEventType(name, event) (types.ts) is `event.toolName === name` for built-in and custom tools alike. The
// production Runner dispatches the tool_call event the Session emits; the handler each SDK registers reports which of four names its guard accepts. The Node SDK runs Pi's own
// function, so every SDK must accept exactly the event's own tool name, isolated and packed.
func TestConformance_ToolCallGuardAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
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
			h.ui.ClearRecorded()
			runConformanceCommandArgs(t, h, "tool_call_guard_probe", "")
			// "mytool" is none of the four names the probe tests, "bashful" only starts with one and "Bash" differs in case: no guard accepts them. The others are accepted by exactly their own name.
			for i, tool := range []string{"bash", "read", "probe", "mytool", "bashful", "Bash"} {
				want := tool
				if tool == "mytool" || tool == "bashful" || tool == "Bash" {
					want = ""
				}
				if _, err := h.runner.EmitToolCall(context.Background(), extension.CustomToolCallEvent{
					ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: tool, Input: map[string]any{},
				}); err != nil {
					t.Fatalf("emit %s: %v", tool, err)
				}
				var reports []string
				pollUntilConformance(t, tool+": the handler never reported its guard", func() bool {
					reports = reports[:0]
					for _, n := range h.ui.Recorded() {
						if rest, ok := strings.CutPrefix(n, "tool_call_guard:"); ok {
							reports = append(reports, strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end"))
						}
					}
					return len(reports) > i
				})
				if reports[i] != want {
					t.Fatalf("names the guard accepts for a %s call = %q, want %q", tool, reports[i], want)
				}
			}
		})
	}
}
