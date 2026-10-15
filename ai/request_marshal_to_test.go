package ai

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The request encoder writes Anthropic content blocks and tools, Responses tools and the Responses body through MarshalJSONTo when the
// json/v2 experiment is on; each must write what its MarshalJSON writes, under encoding/json's own options and inside an enclosing
// value, and the request body (marshalRequestBody) must be JSON.stringify of that.
func TestRequestTypesEncodeAsTheirMarshalJSON(t *testing.T) {
	isError, strict := true, false
	text := "a <b> & \u2028 \xed\xa0\x80 \x00 \"q\""
	values := []json.Marshaler{
		anthContentBlock{Type: "text", Text: text, CacheControl: &anthCacheControl{Type: "ephemeral", TTL: "1h"}},
		anthContentBlock{Type: "text"},
		anthContentBlock{Type: "thinking", Thinking: text},
		anthContentBlock{Type: "redacted_thinking", Data: text},
		anthContentBlock{Type: "tool_use", ID: "toolu_1", Name: "write", Input: json.RawMessage(`{"path":"a<b>.go","content":"` + "x\\n" + `"}`)},
		anthContentBlock{Type: "tool_result", ToolUseID: "toolu_1", Content: []anthContentBlock{{Type: "text", Text: text}}, IsError: &isError},
		anthContentBlock{Type: "image", Source: map[string]any{"type": "base64", "media_type": "image/png", "data": "AAAA"}},
		anthContentBlock{Type: "tool_addition", Tool: &anthToolChange{Type: "tool_definition", Definition: &anthTool{Name: "t", Description: text, InputSchema: map[string]any{"type": "object"}}}},
		anthTool{Name: "read", Description: text, Strict: &strict, EagerInputStream: true, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"z": map[string]any{"type": "string"}, "a": map[string]any{}}}, inputSchemaOrder: schemaObjectOrder{"": {"type", "properties"}, "properties": {"z", "a"}}, CacheControl: &anthCacheControl{Type: "ephemeral"}},
		deferredToolPlaceholder(),
		ResponsesTool{Type: "function", Name: "read", Description: text, Parameters: map[string]any{"type": "object", "b": 1, "a": 2}, parameterOrder: schemaObjectOrder{"": {"type", "b", "a"}}, Strict: json.RawMessage("null"), DeferLoading: true},
		ResponsesTool{Type: "custom", Name: "g", Format: &ResponsesToolFormat{Type: "grammar", Syntax: "lark", Definition: text}},
		respRequest{Model: "m", Input: json.RawMessage(`[{"role":"user","content":"<x> & y"}]`), Stream: true, Store: &strict, Tools: []ResponsesTool{{Type: "function", Name: "n"}}, ToolChoice: "auto", Reasoning: &respReasoning{Effort: "high"}},
		respRequest{Model: "m", Input: json.RawMessage(`[]`), Stream: true, Instructions: text, Text: map[string]string{"verbosity": "low"}, Include: []string{"reasoning.encrypted_content"}, codex: true},
	}
	for i, value := range values {
		want, err := value.MarshalJSON()
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		got, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		// encoding/json rewrites a lone surrogate escape in a marshaler's output to U+FFFD; the request body is encoded without it (marshalStringify),
		// so a value that writes one is held to the request body check only.
		if !bytes.Contains(want, []byte(`\ud800`)) {
			if !bytes.Equal(got, want) {
				t.Errorf("value %d (%T):\n got %s\nwant %s", i, value, got, want)
			}
			nested, err := json.Marshal([]any{value})
			if err != nil || !bytes.Equal(nested, append(append([]byte("["), want...), ']')) {
				t.Errorf("value %d (%T) nested:\n got %s\nwant [%s]", i, value, nested, want)
			}
		}
		body, err := marshalRequestBody([]any{value})
		if err != nil {
			t.Fatal(err)
		}
		stringified, err := marshalRequestBody(json.RawMessage(append(append([]byte("["), want...), ']')))
		if err != nil || !bytes.Equal(body, stringified) {
			t.Errorf("value %d (%T) request body:\n got %s\nwant %s", i, value, body, stringified)
		}
	}
}
