package main

import (
	"strings"
	"testing"
)

func TestAC64FoundationRebaseCarriesUnclosedObligations(t *testing.T) {
	before := map[string]string{"kept": "old", "removed": "gone", "same": "stable"}
	after := map[string]string{"added": "new", "kept": "changed", "same": "stable"}
	changes := computeDelta(before, after)
	if len(changes) != 3 {
		t.Fatalf("changes = %#v", changes)
	}
	for _, item := range changes {
		if item.Disposition != "pending" {
			t.Fatalf("%s disposition = %q", item.ID, item.Disposition)
		}
	}
	manifest := manifest{From: "0.83.0", To: "0.84.0", Changes: changes}
	if problems := validate(manifest, "0.83.0", "0.84.0", changes, false); len(problems) != 0 {
		t.Fatalf("valid manifest problems = %v", problems)
	}
	if problems := validate(manifest, "0.83.0", "0.84.0", changes, true); len(problems) != 3 {
		t.Fatalf("strict pending problems = %v", problems)
	}
}

func TestSemanticDeltaRejectsOmittedOrShapeDriftedChange(t *testing.T) {
	expected := computeDelta(map[string]string{"changed": "before"}, map[string]string{"changed": "after"})
	got := manifest{From: "0.83.0", To: "0.84.0", Changes: append([]change(nil), expected...)}
	got.Changes[0].AfterShapeHash = "fabricated"
	problems := validate(got, "0.83.0", "0.84.0", expected, false)
	if len(problems) != 1 || !strings.Contains(problems[0], "identity/shape drift") {
		t.Fatalf("shape drift problems = %v", problems)
	}
	got.Changes = nil
	problems = validate(got, "0.83.0", "0.84.0", expected, false)
	if len(problems) != 1 || !strings.Contains(problems[0], "changes = 0") {
		t.Fatalf("missing change problems = %v", problems)
	}
}
