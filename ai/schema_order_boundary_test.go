package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestToolSchemaJSONOrderIndicesAndMutation(t *testing.T) {
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"ordered","parameters":{"type":"object","properties":{"z":{"type":"number"},"10":{"type":"string"},"2":{"type":"boolean"},"a":{"type":"string"}}},"constrainedSampling":{"type":"json_schema"}}`), &tool); err != nil {
		t.Fatal(err)
	}
	assert := func(want []string) {
		t.Helper()
		strict, err := getJSONSchemaToolParameters(tool, new(true))
		if err != nil {
			t.Fatal(err)
		}
		if got := toStringSlice(strict["required"]); !reflect.DeepEqual(got, want) {
			t.Fatalf("required=%v want=%v", got, want)
		}
	}
	assert([]string{"2", "10", "z", "a"})
	properties := tool.Parameters["properties"].(map[string]any)
	delete(properties, "z")
	properties["b"] = map[string]any{"type": "string"}
	assert([]string{"2", "10", "a", "b"})
	raw, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatal(err)
	}
	assert([]string{"2", "10", "a", "b"})
	tool.Parameters["cycle"] = tool.Parameters
	if _, err := json.Marshal(tool); err == nil {
		t.Fatal("cyclic schema was accepted")
	}
}

func BenchmarkOrderedToolSchemaRoundTrip(b *testing.B) {
	raw := []byte(`{"name":"ordered","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"},"metadata":{"type":"object","properties":{"enabled":{"type":"boolean"}}}},"required":["path","metadata"]},"constrainedSampling":{"type":"json_schema"}}`)
	b.ReportAllocs()
	for b.Loop() {
		var tool ToolSchema
		if err := json.Unmarshal(raw, &tool); err != nil {
			b.Fatal(err)
		}
		if _, err := getJSONSchemaToolParameters(cloneTool(tool), new(true)); err != nil {
			b.Fatal(err)
		}
		if _, err := json.Marshal(tool); err != nil {
			b.Fatal(err)
		}
	}
}
