package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_EventUnsubscribe pins Pi's `pi.on(event, handler): () => void` (extensions/types.ts:1558) in every SDK: a handler
// registered after the extension connected receives the next event, and once its returned unsubscribe ran, later events do not
// reach it. The handler reports the event's own message entry id, which no SDK's fallback produces; the unsubscribed run
// is followed by a second subscription so the silence is not a handler that never worked.
func TestConformance_EventUnsubscribe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range sdkHarnessCases() {
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
			probes := func() []string {
				var found []string
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, "event_probe:"); ok {
						found = append(found, strings.TrimSuffix(rest, ":info"))
					}
				}
				return found
			}
			emit := func(id string) {
				t.Helper()
				if _, err := h.runner.Emit(ctx, extension.TurnEndEvent{Type: "turn_end", Message: wireAgentMessage(map[string]any{"role": "assistant", "content": []any{}}), MessageEntryID: id, ToolResultEntryIds: []string{"tool-entry"}}); err != nil {
					t.Fatalf("emit turn_end %s: %v", id, err)
				}
			}

			*h.notify = (*h.notify)[:0]
			runConformanceCommand(t, h, "event_probe_subscribe")
			emit("probe-1")
			pollUntilConformance(t, 5*time.Second, "the handler registered after connecting never ran", func() bool {
				return slices.Contains(probes(), "probe-1")
			})

			runConformanceCommand(t, h, "event_probe_unsubscribe")
			emit("probe-2")
			runConformanceCommand(t, h, "event_probe_subscribe")
			emit("probe-3")
			pollUntilConformance(t, 5*time.Second, "the second subscription never ran", func() bool {
				return slices.Contains(probes(), "probe-3")
			})
			if got := probes(); slices.Contains(got, "probe-2") {
				t.Fatalf("an unsubscribed handler received a later event: %v", got)
			}
		})
	}
}
