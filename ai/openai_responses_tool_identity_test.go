package ai

import (
	"fmt"
	"testing"
)

// Pi's processResponsesStream finalizes the existing output slot: output_item.done
// updates arguments/namespace, not the identity created by output_item.added.
func TestResponsesSSEFinalToolIdentityStaysWithOutputSlot(t *testing.T) {
	for _, identity := range []struct {
		name   string
		fields string
	}{
		{"repeated", `"id":"fc_original","call_id":"call_original","name":"read",`},
		{"omitted", ""},
		{"different", `"id":"fc_final","call_id":"call_final","name":"other",`},
	} {
		t.Run(identity.name, func(t *testing.T) {
			sse := fmt.Sprintf(`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_original","call_id":"call_original","name":"read","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"main.go\"}"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call",%s"namespace":"functions","arguments":"{\"path\":\"final.go\"}"}}

data: {"type":"response.completed","response":{"status":"completed"}}

`, identity.fields)
			result, events := collectResponsesEvents(t, sse)
			check := func(call ToolCall) {
				t.Helper()
				if call.ID != "call_original|fc_original" || call.Name != "read" {
					t.Errorf("tool identity changed: %+v", call)
				}
			}
			for _, event := range events {
				switch event := event.(type) {
				case ToolCallStartEvent:
					check(event.Partial.Content[event.ContentIndex].(ToolCall))
				case ToolCallDeltaEvent:
					check(event.Partial.Content[event.ContentIndex].(ToolCall))
				case ToolCallEndEvent:
					check(event.ToolCall)
				}
			}
			call := result.Content[0].(ToolCall)
			check(call)
			if call.Arguments["path"] != "final.go" || call.Namespace != "functions" || result.StopReason != StopReasonToolUse {
				t.Fatalf("finalized call = %+v, stop = %q", call, result.StopReason)
			}
		})
	}
}
