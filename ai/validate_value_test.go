package ai

import (
	"encoding/json"
	"testing"
)

// durable tool.ts structuredOutputError: the strict check of a result's structuredOutput against the tool's schema, whose
// failure names the first TypeBox error's instance path (dotted, "root" for the whole value) and message.
func TestCheckSchemaValue(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"output":   map[string]any{"type": "string"},
			"exitCode": map[string]any{"type": "number"},
			"nested":   map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}, "required": []any{"n"}},
		},
		"required": []any{"output", "exitCode"},
	}
	for _, tc := range []struct {
		name, value, path, message string
		ok                         bool
	}{
		{"matches", `{"output":"x","exitCode":0}`, "", "", true},
		{"wrong member type", `{"output":1,"exitCode":0}`, "output", "must be string", false},
		// No coercion: a numeric string is not a number here, as it would be for a call's arguments.
		{"no coercion", `{"output":"x","exitCode":"0"}`, "exitCode", "must be number", false},
		{"not an object", `[]`, "root", "must be object", false},
		{"missing member is reported at its object", `{"output":"x"}`, "root", "must have required properties exitCode", false},
		{"nested path is dotted", `{"output":"x","exitCode":0,"nested":{"n":"a"}}`, "nested.n", "must be number", false},
		{"missing nested member", `{"output":"x","exitCode":0,"nested":{}}`, "nested", "must have required properties n", false},
	} {
		path, message, ok, err := CheckSchemaValue(schema, json.RawMessage(tc.value))
		if err != nil || ok != tc.ok || path != tc.path || message != tc.message {
			t.Errorf("%s: (%q, %q, %v, %v), want (%q, %q, %v)", tc.name, path, message, ok, err, tc.path, tc.message, tc.ok)
		}
	}
}
