package harness

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 42a3497d harness/tool.ts structuredOutputError and finalResult: a tool declares structuredOutputSchema and returns
// structuredOutput, which the Harness validates strictly; a model-issued call keeps its result and loses the structured
// output, and the break is reported to the host.

var shellLikeSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"output": map[string]any{"type": "string"}, "exitCode": map[string]any{"type": "number"}},
	"required":   []any{"output", "exitCode"},
}

func structuredTool(schema map[string]any) *durable.ToolRegistration {
	tool := registryTool("shell")
	tool.StructuredOutputSchema = schema
	return tool
}

func TestStructuredOutputError(t *testing.T) {
	object := func(output, exitCode any) durable.JsonValue {
		value, err := durable.ToJsonValue(map[string]any{"output": output, "exitCode": exitCode})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	yes := true
	for _, tc := range []struct {
		name   string
		tool   *durable.ToolRegistration
		result durable.ToolExecutionResult
		want   string
	}{
		{"matches", structuredTool(shellLikeSchema), durable.ToolExecutionResult{StructuredOutput: object("x", 0.0), HasStructuredOutput: true}, ""},
		{"no schema and no value", structuredTool(nil), durable.ToolExecutionResult{}, ""},
		{"a value without a schema", structuredTool(nil), durable.ToolExecutionResult{StructuredOutput: object("x", 0.0), HasStructuredOutput: true},
			"Tool shell returned structuredOutput but declares no structuredOutputSchema"},
		{"no value", structuredTool(shellLikeSchema), durable.ToolExecutionResult{}, "Tool shell returned no structuredOutput"},
		{"no value on an error result", structuredTool(shellLikeSchema), durable.ToolExecutionResult{IsError: &yes}, ""},
		{"mismatch", structuredTool(shellLikeSchema), durable.ToolExecutionResult{StructuredOutput: object("x", "0"), HasStructuredOutput: true},
			"Tool shell returned structuredOutput that does not match its schema: exitCode: must be number"},
		{"mismatch at the root", structuredTool(shellLikeSchema), durable.ToolExecutionResult{StructuredOutput: durable.JsonValue([]any{}), HasStructuredOutput: true},
			"Tool shell returned structuredOutput that does not match its schema: root: must be object"},
	} {
		if got := StructuredOutputError(tc.tool, tc.result); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestModelIssuedCallLosesABrokenStructuredOutputAndTheHostLearnsOfIt(t *testing.T) {
	setup := chatSetup(t)
	broken := ownTool("shell", "The shell tool", echoParameters, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		value, _ := durable.ToJsonValue(map[string]any{"output": 1.0, "exitCode": 0.0})
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, StructuredOutput: value, HasStructuredOutput: true}, nil
	}, func(tool *durable.ToolRegistration) { tool.StructuredOutputSchema = shellLikeSchema })
	addTool(t, setup.Registry, broken)
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"shell", map[string]any{}, "c1"}), tlDone()}, nil)
	var result *durable.EntryRecord
	for _, entry := range run.entries {
		if entry.Kind == "pi.tool-result" {
			result = &entry
		}
	}
	if result == nil {
		t.Fatalf("no tool result entry in %d entries", len(run.entries))
	}
	text, _ := textOf(result.Model[0])
	if !strings.Contains(text, "done") || strings.Contains(text, "invalid_structured_output") {
		t.Errorf("the call must stand: %q", text)
	}
	found := false
	for _, report := range setup.Reports.all() {
		found = found || strings.Contains(report.Error(), "Tool shell returned structuredOutput that does not match its schema: output: must be string")
	}
	if !found {
		t.Errorf("reports = %v, want the structuredOutput break", setup.Reports.all())
	}
	mustClose(t, run.harness)
}
