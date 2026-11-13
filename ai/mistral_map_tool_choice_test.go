//go:build !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// mistral-conversations.ts:529 `if (options?.toolChoice) payload.toolChoice = mapToolChoice(options.toolChoice)` and :910-921 mapToolChoice.
func TestMistralToolChoiceMatchesPiMapToolChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice any
		want   any
	}{
		{"absent", nil, nil},
		{"empty string is falsy", "", nil},
		{"auto", "auto", "auto"},
		{"none", ToolChoiceNone, "none"},
		{"any", "any", "any"},
		{"required", "required", "required"},
		{"an extension's object as the JSON text it sent", json.RawMessage(`{"function":{"name":"lookup","extra":1},"type":"function"}`), mistralFunctionChoice{Type: "function", Function: mistralFunctionName{Name: "lookup"}}},
		{"function object keeps only its name", map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "extra": 1}, "other": true}, mistralFunctionChoice{Type: "function", Function: mistralFunctionName{Name: "lookup"}}},
		{"object without a type is rebuilt as a function choice", map[string]any{"function": map[string]any{"name": "lookup"}}, mistralFunctionChoice{Type: "function", Function: mistralFunctionName{Name: "lookup"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mistralToolChoice(tc.choice); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mistralToolChoice(%v) = %#v, want %#v", tc.choice, got, tc.want)
			}
		})
	}
}
