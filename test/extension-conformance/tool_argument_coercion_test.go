package extensionconformance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// Pi validation.ts converts before hooks and execution. Drive the agent path,
// not Definition.Execute directly, for every registered SDK/realization.
func TestToolArgumentCoercionAcrossSDKs(t *testing.T) {
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
			tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{"text": 42, "offset": nil}, &ai.FauxToolCallOptions{ID: "coercion"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			sawHook := false
			a := mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools,
				BeforeToolCallHooks: []agent.BeforeToolCallHook{func(_ context.Context, _, _ string, args json.RawMessage) agent.ToolCallHookResult {
					var input map[string]any
					if err := json.Unmarshal(args, &input); err != nil {
						t.Error(err)
					}
					if _, exists := input["offset"]; exists {
						t.Errorf("optional null survived: %s", args)
					}
					if input["text"] != "42" {
						t.Errorf("hook received raw args: %s", args)
					}
					sawHook = true
					return agent.ToolCallHookResult{}
				}},
			})
			messages, err := a.Send(t.Context(), "echo a number")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, message := range messages {
				if message.Assistant != nil {
					for _, block := range message.Assistant.Content {
						if call, ok := block.(ai.ToolCall); ok {
							if call.Arguments["text"] != float64(42) {
								t.Errorf("history was coerced: %+v", call.Arguments)
							}
							if _, exists := call.Arguments["offset"]; !exists {
								t.Error("history lost original optional null")
							}
						}
					}
				}
				if message.ToolResult != nil {
					found = true
					if message.ToolResult.IsError || message.ToolResult.Text() != "echo: 42" {
						t.Fatalf("tool result = %+v", message.ToolResult)
					}
				}
			}
			if !found || !sawHook {
				t.Fatal("validated tool did not reach hook and execution")
			}
		})
	}
}
