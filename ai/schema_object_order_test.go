package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStrictSchemaRetainsImportedPropertyOrderAcrossTranscriptAndJSON(t *testing.T) {
	var tool ToolSchema
	input := `{"name":"ordered","description":"ordered","parameters":{"type":"object","properties":{"zeta":{"type":"object","properties":{"second":{"type":"string"},"first":{"type":"string"}},"required":["second"]},"alpha":{"type":"string"}},"required":["zeta"]},"constrainedSampling":{"type":"json_schema","strict":"require"}}`
	if err := json.Unmarshal([]byte(input), &tool); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		transcript := NormalizeContext(Context{Tools: []ToolSchema{tool}})
		tool = GetCurrentTools(transcript.Messages())[0]
		schema, err := getJSONSchemaToolParameters(tool, new(true))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(schema["required"], []string{"zeta", "alpha"}) {
			t.Fatalf("required=%#v", schema["required"])
		}
		nested := schema["properties"].(map[string]any)["zeta"].(map[string]any)
		if !reflect.DeepEqual(nested["required"], []string{"second", "first"}) {
			t.Fatalf("nested required=%#v", nested["required"])
		}
		encoded, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(encoded, &tool); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSchemaOrderUsesJavaScriptIntegerKeyEnumeration(t *testing.T) {
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"numeric","parameters":{"type":"object","properties":{"10":{"type":"string"},"2":{"type":"string"},"foo":{"type":"string"},"01":{"type":"string"}},"required":[]}}`), &tool); err != nil {
		t.Fatal(err)
	}
	schema, err := getJSONSchemaToolParameters(tool, new(true))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schema["required"], []string{"2", "10", "foo", "01"}) {
		t.Fatalf("required=%#v", schema["required"])
	}
}

func TestSchemaOrderHandlesEscapedPropertyPathsAndDoesNotAliasStrictOutput(t *testing.T) {
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"paths","parameters":{"type":"object","properties":{"a/b~c":{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"}}}}}}`), &tool); err != nil {
		t.Fatal(err)
	}
	schema, err := getJSONSchemaToolParameters(tool, new(true))
	if err != nil {
		t.Fatal(err)
	}
	nested := schema["properties"].(map[string]any)["a/b~c"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(nested["required"], []string{"z", "a"}) {
		t.Fatalf("nested=%#v", nested)
	}
	nested["required"].([]string)[0] = "mutated"
	second, err := getJSONSchemaToolParameters(tool, new(true))
	if err != nil {
		t.Fatal(err)
	}
	again := second["properties"].(map[string]any)["a/b~c"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(again["required"], []string{"z", "a"}) {
		t.Fatalf("aliased required=%#v", again["required"])
	}
}
