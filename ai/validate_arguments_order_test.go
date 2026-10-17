package ai

import (
	"strings"
	"testing"
)

// validation.ts validateToolArguments reports `Received arguments:\n${JSON.stringify(toolCall.arguments, null, 2)}`: the members
// in the order of the call's arguments object, not sorted.
func TestValidateToolArgumentsReportsTheArgumentsInTheCallsOrder(t *testing.T) {
	tool := ToolSchema{Name: "read", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"},
	}}
	var call ToolCall
	if err := call.SetArgumentsJSON([]byte(`{"zeta":1,"alpha":{"y":2,"b":3}}`)); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateToolArguments(tool, call)
	want := "Received arguments:\n{\n  \"zeta\": 1,\n  \"alpha\": {\n    \"y\": 2,\n    \"b\": 3\n  }\n}"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("validation error = %v, want it to end with %q", err, want)
	}
}
