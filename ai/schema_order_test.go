package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/constrained-sampling.test.ts:129 — derives strict provider schemas without changing tool definitions.
// JSON decoding preserves the same property declaration order as the TypeBox object literal; Go map iteration is not an input-order contract.
func TestConstrainedSamplingStrictSchemaDeclarationOrderUpstream(t *testing.T) {
	const raw = `{"name":"sample_tool","description":"Sample tool","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"},"metadata":{"type":"object","properties":{"enabled":{"type":"boolean"}}},"nullable":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["path","metadata"]},"constrainedSampling":{"type":"json_schema","strict":"prefer"}}`
	var tool ToolSchema
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatal(err)
	}
	encodedTool, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encodedTool, &tool); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(tool.Parameters)
	// Include transcript cloning and the actual tool request converter in the proof.
	transcript := NormalizeContext(Context{Tools: []ToolSchema{tool}})
	cloned := GetCurrentTools(transcript.Messages())[0]
	converted, err := (&openAIResponsesProvider{}).convertTools([]ToolSchema{cloned}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	strict := converted[0].Parameters
	want := map[string]any{"type": "object", "additionalProperties": false, "required": []any{"path", "offset", "metadata", "nullable"}, "properties": map[string]any{
		"path":     map[string]any{"type": "string"},
		"offset":   map[string]any{"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}}},
		"metadata": map[string]any{"type": "object", "additionalProperties": false, "required": []any{"enabled"}, "properties": map[string]any{"enabled": map[string]any{"anyOf": []any{map[string]any{"type": "boolean"}, map[string]any{"type": "null"}}}}},
		"nullable": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}},
	}}
	encoded, err := json.Marshal(strict)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("strict schema=%s", encoded)
	}
	after, _ := json.Marshal(tool.Parameters)
	if string(original) != string(after) {
		t.Fatal("authored schema mutated")
	}
	if _, present := tool.Parameters["additionalProperties"]; present {
		t.Fatal("original additionalProperties changed")
	}
	if !reflect.DeepEqual(toStringSlice(tool.Parameters["required"]), []string{"path", "metadata"}) {
		t.Fatal("original required changed")
	}
}
