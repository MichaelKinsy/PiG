//go:build !pig_strip_codemode

package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi f6ee9ce9 (#11501): packages/coding-agent/test/suite/agent-session-codemode.test.ts "summarizes union and multi-line output types instead of inlining them".
// The schemas are TypeBox's rendering of Pi's inputs: Type.Union of two Type.Object variants with Type.Literal discriminators, Type.Array of Type.Object, and Type.Number.
func TestCodemodeSummarizesUnionAndMultiLineOutputTypesInsteadOfInliningThem(t *testing.T) {
	outputTool := func(name, outputSchema string) (string, extension.RegisteredTool) {
		definition := extension.ToolDefinition{
			Name: name, Label: name, Description: name + " tool", Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
			OutputSchema: json.RawMessage(outputSchema),
			Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				return agent.AgentToolResult{Details: map[string]any{}}, nil
			},
		}
		return name, extension.RegisteredTool{Definition: definition}
	}
	tools := map[string]extension.RegisteredTool{}
	var order []string
	for _, tool := range [][2]string{
		{"process", `{"anyOf":[{"type":"object","properties":{"action":{"const":"start","type":"string"},"process":{"type":"object","properties":{"id":{"description":"Process id","type":"string"},"pid":{"type":"integer"}},"required":["id","pid"]}},"required":["action","process"]},{"type":"object","properties":{"action":{"const":"list","type":"string"},"processes":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}},"truncated":{"type":"boolean"}},"required":["action","processes"]}]}`},
		{"entries", `{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"size":{"type":"integer"}},"required":["id","size"]}}`},
		{"count", `{"type":"number"}`},
	} {
		name, registered := outputTool(tool[0], tool[1])
		tools[name] = registered
		order = append(order, name)
	}
	ext := extension.Extension{Name: "outputs", Path: "<inline:outputs>", ResolvedPath: "<inline:outputs>", Tools: tools, ToolOrder: order}
	h := newCodemodeHarness(t, codemodeHarnessOptions{goExtensions: []extension.Extension{ext}, activeTools: []string{"codemode"}})
	h.session.SetActiveToolsByName([]string{"process", "entries", "count", "codemode"})
	description := func(name string) string {
		for _, tool := range h.session.Tools() {
			if tool.Name() == name {
				return tool.Schema().Description
			}
		}
		return ""
	}
	for name, want := range map[string]string{
		"process": "process tool\n\nCodemode: `tools.process(args)` resolves to `{ action: \"start\", process } | { action: \"list\", processes, truncated? }`.",
		"entries": "entries tool\n\nCodemode: `tools.entries(args)` resolves to a value; `await describeTool(\"entries\")` shows its type.",
		"count":   "count tool\n\nCodemode: `tools.count(args)` resolves to `number`.",
	} {
		if got := description(name); got != want {
			t.Errorf("%s description = %q, want %q", name, got, want)
		}
	}
}
