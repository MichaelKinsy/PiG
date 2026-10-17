package ai

import (
	"maps"
	"testing"
)

// reasoningEffortText reads a request's optional reasoning_effort: "" when the field is absent.
func reasoningEffortText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// openai-completions.ts:1056-1075 resolveChatTemplateKwargValue: a null or scalar value passes through, and every other object, an unknown $var, an empty object or an array included, is the mapped effort.
func TestResolveChatTemplateValuesMapsEveryOtherObjectToTheEffort(t *testing.T) {
	effort := func() (string, bool) { return "high", true }
	got := resolveChatTemplateValues(map[string]any{
		"unknown": map[string]any{"$var": "unknown"},
		"empty":   map[string]any{},
		"array":   []any{"a"},
		"null":    nil,
		"scalar":  "x",
		"omitted": map[string]any{"omitWhenOff": true},
	}, false, effort, 0)
	want := map[string]any{"unknown": "high", "empty": "high", "array": "high", "null": nil, "scalar": "x"}
	if !maps.Equal(got, want) {
		t.Fatalf("resolved %v, want %v", got, want)
	}
}
