package ai

import (
	"encoding/json"
	"testing"
)

// Sections are a JavaScript object in Pi: JSON.parse and JSON.stringify enumerate integer-like keys first, in ascending order, then the others in insertion order (prompt.ts replaySections reads Object.entries of them).
func TestOrderedSectionsEnumerateIntegerKeysFirst(t *testing.T) {
	var sections OrderedSections
	if err := json.Unmarshal([]byte(`{"b":"x","10":"y","a":null,"2":"z"}`), &sections); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, section := range sections {
		names = append(names, section.Name)
	}
	if got, want := names, []string{"2", "10", "b", "a"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("decoded order %q, want %q", got, want)
	}
	value := "v"
	encoded, err := json.Marshal(OrderedSections{{Name: "z", Value: &value}, {Name: "3", Value: &value}, {Name: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"1":null,"3":"v","z":"v"}`; string(encoded) != want {
		t.Fatalf("encoded %s, want %s", encoded, want)
	}
}
