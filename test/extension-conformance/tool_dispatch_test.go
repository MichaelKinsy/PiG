package extensionconformance

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// dispatchToolCalls has the model issue calls in one assistant message and returns the tool results in source order, through
// the agent's production dispatch (prepare, validate, execute) rather than Definition.Execute.
func dispatchToolCalls(t *testing.T, h *harness, calls []ai.FauxContentBlock) []*agent.ToolResultMessage {
	t.Helper()
	tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	a := agent.NewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools})
	messages, err := a.Send(t.Context(), "go")
	if err != nil {
		t.Fatal(err)
	}
	var results []*agent.ToolResultMessage
	for _, message := range messages {
		if message.ToolResult != nil {
			results = append(results, message.ToolResult)
		}
	}
	return results
}

func describeToolResults(results []*agent.ToolResultMessage) string {
	described := make([]string, len(results))
	for i, result := range results {
		described[i] = fmt.Sprintf("%q (error %t)", result.Text(), result.IsError)
	}
	return fmt.Sprint(described)
}

// Upstream agent-loop.ts:707-716 (prepareToolCall): tool.prepareArguments runs before validateToolArguments, so a legacy
// argument shape the schema would reject is rewritten first. Every SDK and realization must reach its tool's hook before
// the host validates, which Definition.PrepareArguments plus Definition.Execute (the old row) never exercised.
func TestToolPrepareArgumentsRunsBeforeValidationAcrossSDKs(t *testing.T) {
	for _, tc := range allHarnessCases() {
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
			results := dispatchToolCalls(t, h, []ai.FauxContentBlock{ai.FauxToolCall("prepared_tool", map[string]any{"legacy": "hello"}, "prepare-1")})
			if len(results) != 1 || results[0].IsError || results[0].Text() != "prepared:hello" {
				t.Fatalf("results = %s, want one prepared:hello: the host validated the raw arguments before the tool's prepareArguments ran", describeToolResults(results))
			}
		})
	}
}

// startOrderStrict says whether the SDK runtime orders the first statements of a batch's handlers, not only their invocation. Node runs the
// handlers on one thread and Python's GIL keeps a thread that just handed its place on running, so both match Pi. A Go or Rust handler runs on
// a thread the operating system can preempt between the hand-off and the handler's first statement, so the SDK guarantees the invocation order
// (the SDK gate tests) and a saturated machine can still let a later handler's first statement run first.
func startOrderStrict(name string) bool {
	switch name {
	case "subprocess-node", "subprocess-node-packed", "subprocess-python":
		return true
	}
	return false
}

// Upstream agent-loop.ts:619-647 and 820-837 (executeToolCallsParallel, executePreparedToolCall): `Promise.all(map)` invokes
// every call of a parallel batch in source order and each reaches tool.execute synchronously, so the calls start in source
// order. The tool numbers its calls as they start; result i must therefore be start#i for argument i-1. The batch is large and
// repeated so a start order that is only usually right fails. Where the runtime cannot order the first statements (see
// startOrderStrict) the row requires every call to start exactly once and the results to stay in source order.
func TestToolBatchStartsInSourceOrderAcrossSDKs(t *testing.T) {
	const batch, rounds = 16, 12
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
			for round := range rounds {
				calls := make([]ai.FauxContentBlock, batch)
				for i := range batch {
					calls[i] = ai.FauxToolCall("start_order", map[string]any{"n": i}, fmt.Sprintf("order-%d-%d", round, i))
				}
				results := dispatchToolCalls(t, h, calls)
				if len(results) != batch {
					t.Fatalf("round %d: %d results, want %d", round, len(results), batch)
				}
				started := make(map[int]bool, batch)
				for i, result := range results {
					if result.IsError {
						t.Fatalf("round %d: call %d failed: %q", round, i, result.Text())
					}
					var number, n int
					if _, err := fmt.Sscanf(result.Text(), "start#%d n=%d", &number, &n); err != nil || n != i {
						t.Fatalf("round %d: result %d = %q, want start#<number> n=%d: the results left source order", round, i, result.Text(), i)
					}
					if number <= round*batch || number > (round+1)*batch || started[number] {
						t.Fatalf("round %d: call %d started as #%d, outside this round's numbers or twice", round, i, number)
					}
					started[number] = true
					if want := round*batch + i + 1; startOrderStrict(tc.name) && number != want {
						t.Fatalf("round %d: call %d started as #%d, want #%d: the batch did not start in source order", round, i, number, want)
					}
				}
			}
		})
	}
}
