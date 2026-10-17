package extensionconformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_EventHandlersDoNotShareTheirPayload is the isolation row of lever A (handoffs/fused-direct-design.md section 5 item 2, review FD-8b). Pi gives each
// extension its own event object (a separate process or realm), so a handler's write to its event can never reach another handler or a later event. In one dispatch the
// first handler writes a sentinel into data.context.contextEntries[0].sourceEntry (a nested container) and, from another goroutine after it returned, writes a second
// one; the second handler of the same dispatch and the first handler's next turn_end (the same payload bytes again) report the marker they received. Every
// realization must report the original marker everywhere. Realizations are the harness cases (in-process Go, subprocess and fused Go, Node, Rust, Python, packed).
func TestConformance_EventHandlersDoNotShareTheirPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	state := boundaryStateFixture()
	state.Context.ContextEntries = []extension.ProjectedSessionEntry{{SourceEntry: map[string]any{"id": "original-marker", "type": "message"}, Messages: []extension.AgentMessage{}}}
	event := extension.TurnEndEvent{BoundaryState: state, Type: "turn_end", TurnIndex: 1,
		Message: wireAgentMessage(map[string]any{"role": "user", "content": "hi", "timestamp": float64(1700000000000)}), ToolResults: []extension.ToolResultMessage{}}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "subprocess-go" && !strings.Contains(tc.name, "go") && !strings.Contains(tc.name, "python") {
				t.Skip("the isolation probe is in the Go and Python fixtures; Node and Rust decode in their own process and have no probe yet")
			}
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			*h.notify = (*h.notify)[:0]
			runConformanceCommandArgs(t, h, "event_isolation_probe", "turn_end")
			reports := func() []string {
				var out []string
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, "event_isolation:"); ok {
						out = append(out, strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end"))
					}
				}
				return out
			}
			for emit := 1; emit <= 2; emit++ {
				if _, err := h.runner.Emit(context.Background(), event); err != nil {
					t.Fatalf("emit %d: %v", emit, err)
				}
				pollUntilConformance(t, "both handlers report", func() bool { return len(reports()) >= 2*emit })
				time.Sleep(300 * time.Millisecond) // the late writer runs 100 ms after the first handler returned, before the next dispatch
			}
			got := reports()
			for _, r := range got {
				if strings.Contains(r, "SENTINEL") || !strings.HasSuffix(r, ":original-marker") {
					t.Errorf("a handler received %q, want the original marker (reports: %v)", r, got)
				}
			}
			if len(got) != 4 {
				t.Errorf("reports = %v, want A and B for each of two dispatches", got)
			}
		})
	}
}
