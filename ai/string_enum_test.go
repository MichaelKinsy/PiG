package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// packages/ai/src/utils/string-enum.ts:14-24 StringEnum: {type: "string", enum: values} plus `description` and `default` only when they are
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

// Pi packages/ai/src/utils/string-enum.ts:14-24: StringEnum is { type: "string", enum, ...(description && { description }),
// ...(default && { default }) } (a pi-schema Schema), the schema Google's API and providers without anyOf/const accept. An empty description or default is omitted
// (JavaScript truthiness), and later changes to the caller's slice do not reach the schema.
func TestStringEnumBuildsPiSchema(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		options *StringEnumOptions
		want    string
	}{
		{"values only", []string{"add", "subtract"}, nil, `{"enum":["add","subtract"],"type":"string"}`},
		{"empty options", []string{"a"}, &StringEnumOptions{}, `{"enum":["a"],"type":"string"}`},
		{"description", []string{"a", "b"}, &StringEnumOptions{Description: "The operation"}, `{"description":"The operation","enum":["a","b"],"type":"string"}`},
		{"default", []string{"a", "b"}, &StringEnumOptions{Default: "b"}, `{"default":"b","enum":["a","b"],"type":"string"}`},
		{"description and default", []string{"a"}, &StringEnumOptions{Description: "d", Default: "a"}, `{"default":"a","description":"d","enum":["a"],"type":"string"}`},
		{"no values", nil, nil, `{"enum":[],"type":"string"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded, err := json.Marshal(StringEnum(c.values, c.options))
			if err != nil || string(encoded) != c.want {
				t.Fatalf("StringEnum = %s (err %v), want %s", encoded, err, c.want)
			}
		})
	}
	values := []string{"a", "b"}
	schema := StringEnum(values, nil)
	values[0] = "changed"
	if got := schema["enum"].([]string)[0]; got != "a" {
		t.Fatalf("schema shares the caller's slice: enum[0] = %q", got)
	}
}
