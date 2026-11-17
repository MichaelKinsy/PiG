package extensionconformance

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// D521 (pig divergence, owner option D, 2026-11-08): a tool whose schema declares a `bigint` parameter receives it as the string `<digits>n` in every SDK, where Pi's tool receives a
// JavaScript bigint (validation.ts:59-61). JSON has no bigint, so every SDK sees a string; the Node runtime's tool gets the same string. Driven through the agent's validation, for each realization.
func TestToolReceivesABigintArgumentAsTheDigitsStringWithNAcrossSDKs(t *testing.T) {
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
			runConformanceCommand(t, h, "bigint_tool")
			waitFor(t, func() bool {
				_, ok := h.runner.GetToolDefinition("bigint_arg")
				return ok
			})
			tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("bigint_arg", map[string]any{"n": "42"}, &ai.FauxToolCallOptions{ID: "bigint"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			a := mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools})
			messages, err := a.Send(t.Context(), "report the bigint")
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range messages {
				if message.ToolResult != nil {
					if message.ToolResult.IsError || message.ToolResult.Text() != "string:42n" {
						t.Fatalf("the tool received %q (error %v), want the string 42n as %q", message.ToolResult.Text(), message.ToolResult.IsError, "string:42n")
					}
					return
				}
			}
			t.Fatal("the tool did not run")
		})
	}
}
