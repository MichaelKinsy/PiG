package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// refineProbeParameters is the parameters JSON of the tool "refine-probe": the JSON Schema Pi's Type writes for a refined string, an optional refined integer, a union whose branches
// are refined strings and an optional string whose predicate throws (the refinements are not part of the JSON). The in-process Go reference lists a tool of that name with it, so every
// transport lists the same tools (TestConformance_TransportsMatch).
func refineProbeParameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["name","pick"],"properties":{"name":{"type":"string"},"qty":{"type":"integer"},"pick":{"anyOf":[{"type":"string"},{"type":"string"}]},"boom":{"type":"string"}}}`)
}

// refineRejectedCalls are tool-call arguments the refinements of "refine-probe" would reject (a name under three characters, an odd qty, a pick outside a and b, a predicate that
// throws). Pi 0.7.0 validates tool arguments with pi-schema, which never runs Type.Refine (packages/coding-agent/CHANGELOG.md: "`Type.Refine()` refinements are not run: arguments
// they would reject are accepted. Check such constraints in execute()"), so every one is valid.
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
// The arguments its refinements would reject validate unchanged, and the extension is never asked to run a predicate or a message function (its call log stays empty).
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
			for _, arguments := range refineRejectedCalls {
				validated, err := ai.ValidateToolArgumentsJSON("probe", def.Parameters, json.RawMessage(arguments))
				if err != nil {
					t.Errorf("%s: %v, Pi accepts: it does not run refinements", arguments, err)
					continue
				}
				var got, want any
				_ = json.Unmarshal(validated, &got)
				_ = json.Unmarshal([]byte(arguments), &want)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: validated to %s, want the arguments unchanged", arguments, validated)
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
