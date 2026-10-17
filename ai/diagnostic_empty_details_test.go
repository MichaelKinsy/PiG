package ai

import (
	"encoding/json"
	"testing"
)

// diagnostics.ts AssistantMessageDiagnostic.details is written whenever it is defined: a `{}` survives a decode and encode, and an absent details stays absent.
func TestAssistantMessageDiagnosticKeepsAnEmptyDetailsObject(t *testing.T) {
	for input, want := range map[string]string{
		`{"type":"t","timestamp":1,"details":{}}`:                         `{"type":"t","timestamp":1,"details":{}}`,
		`{"type":"t","timestamp":1}`:                                      `{"type":"t","timestamp":1}`,
		`{"type":"t","timestamp":1,"details":{"a":1}}`:                    `{"type":"t","timestamp":1,"details":{"a":1}}`,
		`{"type":"t","timestamp":1,"error":{"message":"m"},"details":{}}`: `{"type":"t","timestamp":1,"error":{"message":"m"},"details":{}}`,
	} {
		var diagnostic AssistantMessageDiagnostic
		if err := json.Unmarshal([]byte(input), &diagnostic); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(diagnostic)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s: got %s, want %s", input, got, want)
		}
	}
}
