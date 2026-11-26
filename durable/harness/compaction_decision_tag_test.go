package harness

import (
	"reflect"
	"testing"
)

// Pi 32c71a6d4 tags CompactionDecision with kind: "decline" | "summary" instead of { decline: true } | { summary }.
func TestCompactionDecisionIsTagged(t *testing.T) {
	typ := reflect.TypeFor[CompactionDecision]()
	field, ok := typ.FieldByName("Kind")
	if !ok {
		t.Fatalf("CompactionDecision has no Kind field (fields: %d); want tagged union with Kind", typ.NumField())
	}
	if field.Type.Kind() != reflect.String {
		t.Fatalf("CompactionDecision.Kind has underlying kind %v, want string", field.Type.Kind())
	}
	if _, hasDecline := typ.FieldByName("Decline"); hasDecline {
		t.Fatalf("CompactionDecision still has Decline bool field; Pi 32c71a6d4 replaced it with Kind == decline")
	}
	if CompactionDecline != "decline" || CompactionSummary != "summary" {
		t.Fatalf("kinds = (%q, %q), want (decline, summary)", CompactionDecline, CompactionSummary)
	}
}
