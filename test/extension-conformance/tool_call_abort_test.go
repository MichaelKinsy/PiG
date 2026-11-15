package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi awaits every tool_call handler with no race against the run's abort (agent-loop.ts:738 awaits beforeToolCall = agent-session.ts:719-729 = runner.ts:1256-1274 emitToolCall): the abort reaches the handler as its signal and the handler's result is kept, so the run goes on with it. The probe (tool_call_abort_probe) opens a dialog and answers from the dialog's answer:
//   - mode "wait": the run's abort reaching it while its dialog is open: the dialog is not cancelled, the answer returns, and the handler's reply, a block with the answer as its reason, is the event's result.
//   - mode "first": a handler dispatched after the abort still runs, and its dialog returns the answer.
//
// The reason carries the answer, which no SDK's fallback produces: a handler abandoned at the abort never replies (Emit's error is the cancelled context), and one whose dialog the abort cancelled replies "failed: ...".
func TestConformance_ToolCallHandlerIsAwaitedThroughTheAbort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
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
			h.ui.dialogs = newDialogGate()
			runConformanceCommandArgs(t, h, "tool_call_abort_probe", "")
			for _, mode := range []string{"first", "wait"} {
				t.Run(mode, func(t *testing.T) {
					h.ui.ClearRecorded()
					run, abort := context.WithCancel(context.Background())
					defer abort()
					type outcome struct {
						result *extension.ToolCallEventResult
						err    error
					}
					emitted := make(chan outcome, 1)
					emit := func() {
						result, err := h.runner.EmitToolCall(run, extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "call_1", ToolName: "probe", Input: map[string]any{"mode": mode}})
						emitted <- outcome{result, err}
					}
					pending := "tool_call_abort:" + mode + ":pending"
					held := func() bool {
						if slices.Contains(h.ui.dialogs.openTitles(), pending) {
							return true
						}
						select {
						case o := <-emitted:
							t.Fatalf("EmitToolCall returned before the handler reached its host call: result %+v, err %v", o.result, o.err)
						default:
						}
						return false
					}
					if mode == "first" {
						abort()
						go emit()
						pollUntilConformance(t, "the handler dispatched after the abort never reached its host call", held)
					} else {
						go emit()
						pollUntilConformance(t, "the handler never reached its host call", held)
						// The abort reaches the handler while its call is pending: the host holds the call until it has queued the signal-only cancel frame, which then precedes the answer on the wire.
						cancels := h.wire.frames.signalCancelFrames()
						abort()
						pollUntilConformance(t, "the host never sent the signal-only cancel frame: the abort did not reach the handler as its signal", func() bool { return h.wire.frames.signalCancelFrames() > cancels })
					}
					h.ui.dialogs.answer(t, pending, "answered")
					got := <-emitted
					if got.err != nil {
						t.Fatalf("EmitToolCall: %v", got.err)
					}
					encoded, err := json.Marshal(got.result)
					if err != nil {
						t.Fatal(err)
					}
					if want := fmt.Sprintf("tool_call_abort:%s:answered", mode); got.result == nil || !got.result.Block || got.result.Reason != want {
						t.Fatalf("%s handler's result = %s, want a block with reason %q", mode, encoded, want)
					}
				})
			}
		})
	}
}
