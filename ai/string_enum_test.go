package ai

import (
	"reflect"
	"testing"
)

// packages/ai/src/utils/typebox-helpers.ts:14-24 StringEnum: {type: "string", enum: values} plus `description` and `default` only when they are
// truthy (an empty string is omitted, as `options?.description && {...}` does); the enum is a copy of the values.
func TestStringEnumBuildsThePiSchema(t *testing.T) {
	values := []string{"add", "subtract"}
	cases := []struct {
		name    string
		options *StringEnumOptions
		want    map[string]any
	}{
		{"values only", nil, map[string]any{"type": "string", "enum": []string{"add", "subtract"}}},
		{"description and default", &StringEnumOptions{Description: "The operation", Default: "add"}, map[string]any{"type": "string", "enum": []string{"add", "subtract"}, "description": "The operation", "default": "add"}},
		{"empty strings are omitted", &StringEnumOptions{}, map[string]any{"type": "string", "enum": []string{"add", "subtract"}}},
		{"description only", &StringEnumOptions{Description: "d"}, map[string]any{"type": "string", "enum": []string{"add", "subtract"}, "description": "d"}},
	}
	for _, c := range cases {
		if got := StringEnum(values, c.options); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: StringEnum = %#v, want %#v", c.name, got, c.want)
		}
	}
	schema := StringEnum(values, nil)
	values[0] = "mutated"
	if enum := schema["enum"].([]string); enum[0] != "add" {
		t.Errorf("the enum aliases the caller's slice: %v", enum)
	}
}
