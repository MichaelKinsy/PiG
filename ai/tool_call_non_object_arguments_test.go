package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// json-parse.ts parseStreamingJson returns the value JSON.parse reads, an array, null, a number, a string or a boolean included, and the agent validates and stores
// that value as the call's arguments: the model is told `root: must be object ... Received arguments: []`, and the session keeps `"arguments":[]`.
func TestStreamedToolArgumentsThatAreNotAnObjectAreKeptAsPiKeepsThem(t *testing.T) {
	tool := ToolSchema{Name: "read", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"},
	}}
	for _, text := range []string{`[]`, `null`, `5`, `"s"`, `true`, `[{"b":1,"a":2}]`} {
		t.Run(text, func(t *testing.T) {
			var call ToolCall
			call.ID, call.Name = "c1", "read"
			call.SetStreamingArguments(text)
			if call.Arguments != nil {
				t.Fatalf("Arguments = %#v, want none for the non-object value", call.Arguments)
			}
			arguments, err := call.ArgumentsJSON()
			if err != nil || string(arguments) != text {
				t.Fatalf("ArgumentsJSON = %s, %v; want %s", arguments, err, text)
			}
			_, err = ValidateToolArguments(tool, call)
			if err == nil || !strings.Contains(err.Error(), "root: must be object") || (!strings.Contains(text, "{") && !strings.Contains(err.Error(), "Received arguments:\n"+text)) {
				t.Fatalf("validation error = %v, want root: must be object and the received value %s", err, text)
			}
			if _, err := ValidateToolArgumentsJSON("read", nil, arguments); err == nil {
				t.Fatal("the agent's validation accepted a non-object value")
			}
			encoded, err := json.Marshal(call)
			if err != nil || !strings.Contains(string(encoded), `"arguments":`+text) {
				t.Fatalf("session JSON = %s, %v; want arguments %s", encoded, err, text)
			}
			var decoded ToolCall
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if again, err := decoded.ArgumentsJSON(); err != nil || string(again) != text {
				t.Fatalf("round trip ArgumentsJSON = %s, %v; want %s", again, err, text)
			}
		})
	}
	var object ToolCall
	object.SetStreamingArguments(`{"path":"a"}`)
	if arguments, _ := object.ArgumentsJSON(); string(arguments) != `{"path":"a"}` || object.nonObjectArguments != nil {
		t.Fatalf("an object call = %s, %s", arguments, object.nonObjectArguments)
	}
	var partial ToolCall
	partial.SetStreamingArguments(`nul`)
	if arguments, _ := partial.ArgumentsJSON(); string(arguments) != `{}` {
		t.Fatalf("a partial literal is the empty object (result ?? {}); got %s", arguments)
	}
}
