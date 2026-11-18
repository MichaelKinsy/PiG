package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi runs tool.execute with an aborted signal when it is called with one (subagent-extension.test.ts "returns aborted task details when the signal is already aborted"),
// and the tool answers for itself. A tool call whose context had ended when it reached the host (a direct call, or an abort that lands after the agent's gate) is
// therefore sent to the extension, in every SDK, isolated, packed and fused, with its signal already aborted on the tool's first line, and D111's rule applies from
// the abort: the tool's host calls are answered, a tool that answers within the grace period gives its own result, and one that does not ends as "Operation aborted".
// aborted_entry_probe echoes a nonce the test makes up, reports whether its signal was aborted on its first line, and asks the host one dialog after that: its result,
// "aborted_entry:<nonce>:true:<answer>", is not produced by a host that refuses the call, an SDK that starts the tool with a live signal, or one that never runs it.
func TestConformance_ToolCalledAbortedEntersWithItsSignalAborted(t *testing.T) {
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
			tool, ok := findTool(h.runner, "aborted_entry_probe")
			if !ok {
				t.Fatal("aborted_entry_probe not registered")
			}
			type outcome struct {
				result agent.AgentToolResult
				err    error
			}
			// call runs the tool under a context that ended before the call began and returns its outcome and the dialog's title.
			call := func(t *testing.T) (<-chan outcome, string) {
				t.Helper()
				nonce := fmt.Sprintf("n%d", time.Now().UnixNano())
				title := "aborted_entry:" + nonce
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				requests, cancels := h.wire.frames.abortedToolCallRequests(), h.wire.frames.signalCancelFrames()
				done := make(chan outcome, 1)
				go func() {
					result, err := tool.Definition.Execute(ctx, "tc-aborted-entry", json.RawMessage(fmt.Sprintf(`{"nonce":%q}`, nonce)), func(agent.AgentToolResult) {})
					done <- outcome{result, err}
				}()
				pollUntilConformance(t, "the tool's dialog after its aborted entry never reached the host", func() bool { return slices.Contains(h.ui.dialogs.openTitles(), title) })
				if got := h.wire.frames.abortedToolCallRequests(); got != requests+1 {
					t.Fatalf("the host wrote %d tool_call requests with signal_aborted, want 1", got-requests)
				}
				pollUntilConformance(t, "the signal-only cancel never followed the request", func() bool { return h.wire.frames.signalCancelFrames() > cancels })
				return done, title
			}
			result := func(t *testing.T, done <-chan outcome) outcome {
				t.Helper()
				select {
				case got := <-done:
					return got
				case <-time.After(subprocess.ToolAbortGrace + 45*time.Second):
					t.Fatal("the tool did not settle")
				}
				return outcome{}
			}

			t.Run("tool_answers_within_the_grace_period", func(t *testing.T) {
				done, title := call(t)
				h.ui.dialogs.answer(t, title, "ok")
				got := result(t, done)
				if want := title + ":true:ok"; got.err != nil || got.result.Text() != want {
					t.Fatalf("aborted_entry_probe = %q, %v; want %q", got.result.Text(), got.err, want)
				}
			})

			t.Run("tool_that_does_not_settle_ends_as_aborted", func(t *testing.T) {
				done, title := call(t)
				got := result(t, done)
				if got.err == nil || got.err.Error() != "Operation aborted" {
					t.Fatalf("aborted_entry_probe = %q, %v; want the grace's %q", got.result.Text(), got.err, "Operation aborted")
				}
				pollUntilConformance(t, "the abandoned tool's dialog stayed open", func() bool { return !slices.Contains(h.ui.dialogs.openTitles(), title) })
			})
		})
	}
}
