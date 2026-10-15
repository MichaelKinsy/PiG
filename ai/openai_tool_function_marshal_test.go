package ai

import (
	"bytes"
	"encoding/json"
	"testing"
)

// oaiToolsForEncoding is a completions tool list like the coding agent's: function tools whose schemas keep their own member order.
func oaiToolsForEncoding(count int) []oaiTool {
	strict := false
	tools := make([]oaiTool, count)
	for i := range tools {
		tools[i] = oaiTool{Type: "function", Function: &oaiToolFunction{
			Name: "read", Description: "Read a file <path> & print it \u2028",
			Parameters:     map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "offset": map[string]any{"type": "number"}}, "required": []any{"path"}},
			parameterOrder: schemaObjectOrder{"": {"type", "properties", "required"}, "properties": {"path", "offset"}},
			Strict:         &strict,
		}}
	}
	tools[0].CacheControl = &oaiCacheControl{Type: "ephemeral"}
	return tools
}

// oaiToolFunction writes what its MarshalJSON writes when the request encoder calls MarshalJSONTo: alone, nested, and as JSON.stringify in a
// request body.
func TestOpenAIToolFunctionEncodesAsItsMarshalJSON(t *testing.T) {
	strict := true
	values := []json.Marshaler{
		oaiToolsForEncoding(1)[0].Function,
		oaiToolFunction{Name: "n", Description: "a <b> & \u2028 \xed\xa0\x80 \x00 \"q\"", Parameters: map[string]any{"b": 1, "a": []any{"x"}}, Strict: &strict},
		oaiToolFunction{Name: "n"},
	}
	for i, value := range values {
		want, err := value.MarshalJSON()
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		got, err := json.Marshal(value)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("value %d:\n got %s\nwant %s (%v)", i, got, want, err)
		}
		nested, err := json.Marshal([]any{oaiTool{Type: "function"}, value})
		if wantNested := `[{"type":"function"},` + string(want) + `]`; err != nil || string(nested) != wantNested {
			t.Errorf("value %d nested:\n got %s\nwant %s (%v)", i, nested, wantNested, err)
		}
		body, err := marshalRequestBody([]any{value})
		if err != nil {
			t.Fatal(err)
		}
		stringified, err := marshalRequestBody(json.RawMessage(append(append([]byte("["), want...), ']')))
		if err != nil || !bytes.Equal(body, stringified) {
			t.Errorf("value %d request body:\n got %s\nwant %s", i, body, stringified)
		}
	}
}
