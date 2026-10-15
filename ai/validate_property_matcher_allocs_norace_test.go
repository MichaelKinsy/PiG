//go:build !race && !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TypeBox's Convert matches each property name of an Object schema as `^name$` against every key of the value (typeboxPropertyNameMatcher). The pattern depends only on the schema, so it compiles once
// per cached schema: validating one more array item of a two-property object costs the item's own conversion and validation (about 96 allocations measured), not a regexp compile per property.
func TestValidateToolArgsComparesPropertyNamesWithoutCompilingPerItem(t *testing.T) {
	schema := json.RawMessage(`{"~kind":"Object","type":"object","properties":{"edits":{"~kind":"Array","type":"array","items":{"~kind":"Object","type":"object","properties":{"oldText":{"~kind":"String","type":"string"},"newText":{"~kind":"String","type":"string"}},"required":["oldText","newText"]}}},"required":["edits"]}`)
	args := func(items int) json.RawMessage {
		return json.RawMessage(`{"edits":[` + strings.TrimSuffix(strings.Repeat(`{"oldText":42,"newText":true},`, items), ",") + `]}`)
	}
	validate := func(raw json.RawMessage) float64 {
		return testing.AllocsPerRun(5, func() {
			if _, err := validateToolArgsSchema("edit", schema, raw); err != nil {
				t.Fatal(err)
			}
		})
	}
	one, many := validate(args(1)), validate(args(101))
	if perItem := (many - one) / 100; perItem > 120 {
		t.Fatalf("one more edit costs %.0f allocations, want at most 120 (the schema's property-name patterns are compiled per object)", perItem)
	}
}
