package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi awaits every handler of session_before_compact and session_before_tree with event.signal aborted and keeps their results (runner.ts emit; agent-session.ts:2878-2890, 3206-3218, 4107-4135; navigateTree checks no abort after the emit). Every SDK's handler therefore sees
//   - mode "wait": the abort reaching it while it runs (event.signal aborts, the handler's request is cancelled) and its reply, returned after the abort, reaches the host;
//   - mode "first": a handler dispatched after the abort starts with the signal already aborted, on its first line.
//
// The probe returns the state it read in its result, which no SDK's fallback produces: a handler that never ran, a reply the host dropped and a signal that was not aborted all give another value. The row runs in every SDK, isolated, packed and (Go) fused, with the dispatch context detached from the abort as the Session emits the events.
func TestSessionBeforeSignalReachesEverySDKWhenAbortedDuringDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	events := []struct {
		name string
		// emit builds the event for the mode, whose signal is signal.
		emit func(mode string, signal context.Context) extension.ExtensionEvent
		// state reads the probe's state out of its result.
		state func(result map[string]any) string
	}{
		{"session_before_compact", func(mode string, signal context.Context) extension.ExtensionEvent {
			return extension.SessionBeforeCompactEvent{Type: "session_before_compact", BranchEntries: []extension.SessionEntry{}, CustomInstructions: mode, Reason: "manual", Signal: signal}
		}, func(result map[string]any) string {
			compaction, _ := result["compaction"].(map[string]any)
			summary, _ := compaction["summary"].(string)
			return summary
		}},
		{"session_before_tree", func(mode string, signal context.Context) extension.ExtensionEvent {
			return extension.SessionBeforeTreeEvent{Type: "session_before_tree", Preparation: extension.TreePreparation{TargetID: "t1", EntriesToSummarize: []extension.SessionEntry{}, CustomInstructions: mode}, Signal: signal}
		}, func(result map[string]any) string {
			label, _ := result["label"].(string)
			return label
		}},
	}
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
			for _, event := range events {
				t.Run(event.name, func(t *testing.T) {
					runConformanceCommandArgs(t, h, "event_signal_probe", event.name)
					for _, mode := range []string{"first", "wait"} {
						t.Run(mode, func(t *testing.T) {
							h.ui.ClearRecorded()
							signal, abort := context.WithCancel(context.Background())
							defer abort()
							type outcome struct {
								result any
								err    error
							}
							emitted := make(chan outcome, 1)
							emit := func() {
								result, err := h.runner.Emit(context.WithoutCancel(signal), event.emit(mode, signal))
								emitted <- outcome{result, err}
							}
							if mode == "first" {
								abort()
								emit()
							} else {
								go emit()
								entered := "event_signal:" + event.name + ":entered:info"
								pollUntilConformance(t, "the handler never entered its wait", func() bool {
									return slices.Contains(h.ui.Recorded(), entered)
								})
								abort()
							}
							got := <-emitted
							if got.err != nil {
								t.Fatalf("Emit: %v", got.err)
							}
							encoded, err := json.Marshal(got.result)
							if err != nil {
								t.Fatal(err)
							}
							var decoded map[string]any
							_ = json.Unmarshal(encoded, &decoded)
							if want := fmt.Sprintf("event_signal:%s:true", mode); event.state(decoded) != want {
								t.Fatalf("%s handler reported %q (result %s), want %q", mode, event.state(decoded), encoded, want)
							}
						})
					}
				})
			}
		})
	}
}
