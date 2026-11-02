package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// refineProbeParameters is the parameters JSON of the tool "refine-probe": the JSON Schema Pi's Type writes for a refined string, an optional refined integer, a union whose branches
// are refined strings and an optional string whose predicate throws (the refinements are not part of the JSON). The in-process Go reference lists a tool of that name with it, so every
// transport lists the same tools (TestConformance_TransportsMatch).
func refineProbeParameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["name","pick"],"properties":{"name":{"type":"string"},"qty":{"type":"integer"},"pick":{"anyOf":[{"type":"string"},{"type":"string"}]},"boom":{"type":"string"}}}`)
}

// refineRejectedCalls are tool-call arguments the refinements of "refine-probe" would reject (a name under three characters, an odd qty, a pick outside a and b, a predicate that
// throws). Pi (1.4.0+b556cfe5) validates tool arguments with pi-schema, which never runs Type.Refine (packages/ai/CHANGELOG.md:25 and packages/coding-agent/docs/extensions.md:176:
// "`Type.Refine()` refinements are not run: arguments they would reject are accepted. Check such constraints in execute()"), so every one is valid and the tool runs.
var refineRejectedCalls = []string{
	`{"name":"ab","pick":"a"}`,
	`{"name":"abc","qty":3,"pick":"a"}`,
	`{"name":"abc","pick":"c"}`,
	`{"name":"x","qty":1,"pick":"z"}`,
	`{"name":"abc","pick":"a","boom":"x"}`,
}

// assertNoRefinementProtocol fails when the tool definition still carries the protocol that ran refinements in the extension: Pi's validation never consults them.
func assertNoRefinementProtocol(t *testing.T, def extension.ToolDefinition) {
	t.Helper()
	typ := reflect.TypeOf(def)
	for _, field := range []string{"RefinementSites", "RefineArguments"} {
		if _, found := typ.FieldByName(field); found {
			t.Errorf("ToolDefinition has the field %s: the host no longer asks an extension to run refinements", field)
		}
	}
}

// TestConformance_RefinedToolArgumentsAreNotRefined: every SDK registers the tool "refine-probe" with parameters built by Type and Type.Refine (the Node runtime registers Pi's own).
// Each call its refinements would reject goes through the host as the agent loop issues it (validation, hooks, execution of the tool). The tool must run and answer "ok", and the extension
// is never asked to run a predicate or a message function: its call log stays empty after the calls, so a host or runtime that ran a refinement would fail here.
func TestConformance_RefinedToolArgumentsAreNotRefined(t *testing.T) {
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
			var def *extension.ToolDefinition
			for _, tool := range h.runner.Tools() {
				if tool.Definition.Name == "refine-probe" {
					definition := tool.Definition
					def = &definition
				}
			}
			if def == nil {
				t.Fatal("refine-probe is not registered")
			}
			assertNoRefinementProtocol(t, *def)
			tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			probeCalls := make([]ai.FauxContentBlock, len(refineRejectedCalls))
			for i, arguments := range refineRejectedCalls {
				var input map[string]any
				if err := json.Unmarshal([]byte(arguments), &input); err != nil {
					t.Fatal(err)
				}
				probeCalls[i] = ai.FauxToolCall("refine-probe", input, &ai.FauxToolCallOptions{ID: fmt.Sprintf("refine-%d", i)})
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: probeCalls, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			a := mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools})
			messages, err := a.Send(t.Context(), "call the probe")
			if err != nil {
				t.Fatal(err)
			}
			results := map[string]string{}
			for _, message := range messages {
				if message.ToolResult != nil {
					if message.ToolResult.IsError {
						t.Errorf("tool call %s failed: %s, Pi accepts the arguments: it does not run refinements", message.ToolResult.ToolCallID, message.ToolResult.Text())
					}
					results[message.ToolResult.ToolCallID] = message.ToolResult.Text()
				}
			}
			for i, arguments := range refineRejectedCalls {
				if got := results[fmt.Sprintf("refine-%d", i)]; got != "ok" {
					t.Errorf("%s: the tool answered %q, want \"ok\" (the call is valid and the tool ran)", arguments, got)
				}
			}
			before := len(*h.notify)
			if !h.runner.ExecuteCommand(context.Background(), "refine_log", "") {
				t.Fatal("no refine_log command")
			}
			var reported string
			pollUntilConformance(t, "refine_log: no report", func() bool {
				for _, n := range (*h.notify)[before:] {
					if rest, ok := strings.CutPrefix(n, "refine_log:"); ok {
						reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			var calls []string
			if err := json.Unmarshal([]byte(reported), &calls); err != nil {
				t.Fatalf("log %q: %v", reported, err)
			}
			if len(calls) != 0 {
				t.Errorf("the extension ran refinements %v; the host never asks for them", calls)
			}
		})
	}
}
