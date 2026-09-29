package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// transcript.ts:123-129 keeps `constrainedSampling: false` in tool declarations; only `undefined` drops the key.
func TestToolSchemaKeepsExplicitConstrainedSamplingFalse(t *testing.T) {
	const withFalse = `{"name":"t","description":"d","parameters":{"type":"object"},"constrainedSampling":false}`
	var tool ToolSchema
	if err := json.Unmarshal([]byte(withFalse), &tool); err != nil {
		t.Fatal(err)
	}
	if !tool.ConstrainedSamplingDisabled || tool.ConstrainedSampling != nil {
		t.Fatalf("decoded = %#v", tool)
	}
	for label, value := range map[string]ToolSchema{"decoded": tool, "declaration": ToToolDeclaration(tool), "clone": cloneTool(tool)} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"constrainedSampling":false`) {
			t.Errorf("%s dropped the explicit false: %s", label, encoded)
		}
	}
	absent := ToolSchema{Name: "t", Description: "d", Parameters: map[string]any{"type": "object"}}
	encoded, err := json.Marshal(absent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "constrainedSampling") {
		t.Errorf("absent property was written: %s", encoded)
	}
	if DeclarationsEqual(tool, absent) {
		t.Error("false and undefined declarations compare equal; Pi's JSON comparison differs")
	}
}
