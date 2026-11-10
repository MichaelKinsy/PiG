package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's abort of a run only aborts the signal of the tool in flight (agent-loop.ts:817-853 awaits tool.execute with it): the tool's in-process host calls,
// pending at the abort or made after it, complete, and a nested executeTool without a signal of its own runs with the outer tool's signal, so the abort
// reaches it (runner.ts:979-981 `options.signal ?? signal`). Every SDK, isolated, packed and fused, must therefore:
//   - answer the dialog a tool awaits when the run aborts, and the one it opens after the abort, and hand the tool its signal aborted
//     (signal_host_probe returns "tool_signal:true:one:two", which no fallback produces: a failed call, a refused call and a tool that never ran each give another text);
//   - abort the nested abort_tool, which then returns its own "aborted" (signal_nested_probe returns "tool_signal_nested:aborted");
//   - cancel what a tool still awaits when D111's grace ends (the dialog is closed, no pending call outlives the abandoned tool).
func TestToolAbortKeepsTheToolsHostCalls(t *testing.T) {
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
			// start runs the tool under a context the test can abort and returns its outcome.
			type outcome struct {
				result agent.AgentToolResult
				err    error
			}
			start := func(t *testing.T, name string) (abort func(), done <-chan outcome) {
				tool, ok := findTool(h.runner, name)
				if !ok {
					t.Fatalf("%s not registered", name)
				}
				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)
				out := make(chan outcome, 1)
				go func() {
					result, err := tool.Definition.Execute(ctx, "tc-"+name, json.RawMessage(`{}`), func(agent.AgentToolResult) {})
					out <- outcome{result, err}
				}()
				return cancel, out
			}
			held := func(title string) func() bool {
				return func() bool { return slices.Contains(h.ui.dialogs.openTitles(), title) }
			}
			// abortAndWaitForCancel aborts the run and waits until the host has queued the signal-only cancel frame, which precedes every later frame on the wire.
			abortAndWaitForCancel := func(t *testing.T, abort func()) {
				cancels := h.wire.frames.signalCancelFrames()
				abort()
				pollUntilConformance(t, "the host never sent the signal-only cancel frame", func() bool { return h.wire.frames.signalCancelFrames() > cancels })
			}
			result := func(t *testing.T, done <-chan outcome) outcome {
				select {
				case got := <-done:
					return got
				case <-time.After(subprocess.ToolAbortGrace + 45*time.Second):
					t.Fatal("the tool did not settle")
				}
				return outcome{}
			}

			t.Run("pending_and_later_calls", func(t *testing.T) {
				abort, done := start(t, "signal_host_probe")
				pollUntilConformance(t, "the tool never reached its first dialog", held("tool_signal:first"))
				abortAndWaitForCancel(t, abort)
				h.ui.dialogs.answer(t, "tool_signal:first", "one")
				pollUntilConformance(t, "the tool's dialog after the abort never reached the host", held("tool_signal:later"))
				h.ui.dialogs.answer(t, "tool_signal:later", "two")
				got := result(t, done)
				if got.err != nil || got.result.Text() != "tool_signal:true:one:two" {
					t.Fatalf("signal_host_probe = %q, %v; want %q", got.result.Text(), got.err, "tool_signal:true:one:two")
				}
			})

			t.Run("nested_tool_sees_the_abort", func(t *testing.T) {
				calls := h.wire.frames.toolCallRequests()
				abort, done := start(t, "signal_nested_probe")
				pollUntilConformance(t, "the nested tool call never reached its extension", func() bool { return h.wire.frames.toolCallRequests() >= calls+2 })
				abortAndWaitForCancel(t, abort)
				got := result(t, done)
				if got.err != nil || got.result.Text() != "tool_signal_nested:aborted" {
					t.Fatalf("signal_nested_probe = %q, %v; want %q", got.result.Text(), got.err, "tool_signal_nested:aborted")
				}
			})

			t.Run("grace_end_cancels_what_the_tool_awaits", func(t *testing.T) {
				abort, done := start(t, "signal_host_probe")
				pollUntilConformance(t, "the tool never reached its first dialog", held("tool_signal:first"))
				abortAndWaitForCancel(t, abort)
				if !held("tool_signal:first")() {
					t.Fatal("the abort closed the dialog the tool awaits; only the end of the grace period may")
				}
				got := result(t, done)
				if got.err == nil || got.err.Error() != "Operation aborted" {
					t.Fatalf("signal_host_probe = %q, %v; want the grace's %q", got.result.Text(), got.err, "Operation aborted")
				}
				pollUntilConformance(t, "the abandoned tool's dialog stayed open", func() bool { return !held("tool_signal:first")() })
			})
		})
	}
}
