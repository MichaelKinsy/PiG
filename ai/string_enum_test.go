package ai

import (
	"encoding/json"
	"testing"
)

// packages/ai/src/utils/string-enum.ts:14-24 StringEnum is `{ type: "string", enum: values, ...(description && { description }), ...(default && { default }) }`, so
// JSON.stringify writes type, enum, description, default in that order, and an empty description or default is omitted (JavaScript truthiness). The enum is the caller's array,
// not a copy, so a later change to it reaches the schema. Pi's `Type.Object({ mode: StringEnum([...]) })` bytes were probed with the vendored Pi 1.5.0 pi-ai.
func TestStringEnumWritesTheMembersInPiOrder(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		options *StringEnumOptions
		want    string
	}{
		{"values only", []string{"add", "subtract"}, nil, `{"type":"string","enum":["add","subtract"]}`},
		{"empty options", []string{"a"}, &StringEnumOptions{}, `{"type":"string","enum":["a"]}`},
		{"description", []string{"a", "b"}, &StringEnumOptions{Description: "The operation"}, `{"type":"string","enum":["a","b"],"description":"The operation"}`},
		{"default", []string{"a", "b"}, &StringEnumOptions{Default: "b"}, `{"type":"string","enum":["a","b"],"default":"b"}`},
		{"description and default", []string{"a"}, &StringEnumOptions{Description: "d", Default: "a"}, `{"type":"string","enum":["a"],"description":"d","default":"a"}`},
		{"no values", nil, nil, `{"type":"string","enum":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded, err := json.Marshal(StringEnum(c.values, c.options))
			if err != nil || string(encoded) != c.want {
				t.Fatalf("StringEnum = %s (err %v), want %s", encoded, err, c.want)
			}
		})
	}
}

// string-enum.ts:20 `enum: values as any` shares the caller's array: a change to it after StringEnum returns is visible in the schema.
func TestStringEnumSharesTheCallersValues(t *testing.T) {
	values := []string{"a", "b"}
	schema := StringEnum(values, nil)
	values[0] = "changed"
	encoded, err := json.Marshal(schema)
	if want := `{"type":"string","enum":["changed","b"]}`; err != nil || string(encoded) != want {
		t.Fatalf("StringEnum after the caller changed its values = %s (err %v), want %s", encoded, err, want)
	}
}
