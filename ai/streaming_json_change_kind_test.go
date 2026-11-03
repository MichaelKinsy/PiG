package ai

import "testing"

// Pi utils/streaming-json.ts:51-53 `JsonChange = { kind: "set", ... } | { kind: "append", ... }`: the kind is a closed union of two
// literals, a named Go string type with a constant for each.
func TestJSONChangeKindIsAClosedTypeOfSetAndAppend(t *testing.T) {
	var parser StreamingJSONParser
	changes := parser.Append(`{"a":"x`)
	changes = append(changes, parser.Append(`yz"}`)...)
	var kinds []JSONChangeKind
	for _, change := range changes {
		kinds = append(kinds, change.Kind)
	}
	if len(kinds) < 2 || kinds[0] != JSONChangeSet {
		t.Fatalf("kinds = %v, want a first set", kinds)
	}
	var sawAppend bool
	for _, kind := range kinds {
		sawAppend = sawAppend || kind == JSONChangeAppend
	}
	if !sawAppend || JSONChangeSet != "set" || JSONChangeAppend != "append" {
		t.Errorf("kinds = %v, set %q, append %q", kinds, JSONChangeSet, JSONChangeAppend)
	}
}
