package ai

import (
	"encoding/json"
	"testing"
)

// Pi packages/ai/src/utils/typebox-helpers.ts:14-24: StringEnum is Type.Unsafe({ type: "string", enum, ...(description && { description }),
// ...(default && { default }) }), the schema Google's API and providers without anyOf/const accept. An empty description or default is omitted
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
