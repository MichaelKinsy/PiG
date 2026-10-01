package inproc

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A tool_result handler's `details` replaces the tool's (runner.ts:707-755). An extension process returns it as JSON the host decodes; the chained and returned value keeps the member order the handler wrote, as the JavaScript object it is does.
func TestEmitToolResultKeepsHandlerDetailsMemberOrder(t *testing.T) {
	const details = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`
	runner := NewRunner([]extension.Extension{{Path: "/tmp/order.ts", Handlers: map[string][]extension.HandlerFn{
		"tool_result": {func(args ...any) (any, error) { return json.RawMessage(`{"details":` + details + `}`), nil }},
	}}}, ".")
	result, err := runner.EmitToolResult(t.Context(), extension.CustomToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "call"},
		ToolName:            "tool",
	})
	if err != nil || result == nil {
		t.Fatalf("EmitToolResult = %#v, %v", result, err)
	}
	if encoded, err := json.Marshal(result.Details); err != nil || string(encoded) != details {
		t.Fatalf("details = %s, %v, want %s", encoded, err, details)
	}
}
