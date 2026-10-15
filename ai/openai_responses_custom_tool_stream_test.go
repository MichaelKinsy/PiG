package ai

import (
	"strings"
	"testing"
)

// openai-responses-shared.ts processResponsesStream custom_tool_call items (grammar tools): the input starts from the item's input, each
// custom_tool_call_input.delta appends, custom_tool_call_input.done replaces it, and output_item.done keeps the last input unless the item
// carries one. The arguments name the input under the tool's grammar property ("input" when none is declared).
func TestProcessResponsesStreamCustomToolCallInputAccumulates(t *testing.T) {
	added := func(input string) string {
		item := `{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"apply_patch"` + input + `}}`
		return "data: " + item + "\n\n"
	}
	event := func(body string) string { return "data: " + body + "\n\n" }
	done := func(input string) string {
		return event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"apply_patch"` + input + `}}`)
	}
	completed := event(`{"type":"response.completed","response":{"status":"completed"}}`)
	run := func(properties map[string]string, events ...string) ToolCall {
		t.Helper()
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "gpt-test")
		go processResponsesStream(t.Context(), newSSEDecoder(strings.NewReader(strings.Join(events, ""))), builder, &OpenAIResponsesStreamOptions{model: &Model{ID: "gpt-test"}, GrammarToolInputProperties: properties})
		result := builder.stream.Result()
		if result.StopReason != StopReasonToolUse || len(result.Content) != 1 {
			t.Fatalf("result = %+v", result)
		}
		call, ok := result.Content[0].(ToolCall)
		if !ok {
			t.Fatalf("content = %#v", result.Content[0])
		}
		return call
	}
	patch := map[string]string{"apply_patch": "patch"}
	if got := run(patch, added(`,"input":"ab"`), event(`{"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"c"}`), done(``), completed).Arguments["patch"]; got != "abc" {
		t.Errorf("an item input then a delta = %q, want %q", got, "abc")
	}
	if got := run(patch, added(``), event(`{"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"fu"}`),
		event(`{"type":"response.custom_tool_call_input.done","output_index":0,"input":"full"}`), done(``), completed).Arguments["patch"]; got != "full" {
		t.Errorf("input.done = %q, want %q", got, "full")
	}
	if got := run(patch, added(``), event(`{"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"fi"}`), done(`,"input":"final"`), completed).Arguments["patch"]; got != "final" {
		t.Errorf("an item input on output_item.done = %q, want %q", got, "final")
	}
	if got := run(nil, added(`,"input":"x"`), done(``), completed).Arguments["input"]; got != "x" {
		t.Errorf("a tool without a declared grammar property = %#v, want the argument named input", got)
	}
}
