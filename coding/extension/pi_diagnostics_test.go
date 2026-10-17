package extension

import (
	"encoding/json"
	"testing"
)

// pi: packages/coding-agent/src/core/diagnostics.ts
//
// core/diagnostics.ts declares two interfaces, so the contract is their JSON shape: ResourceCollision {resourceType, name, winnerPath, loserPath, winnerSource?,
// loserSource?} and ResourceDiagnostic {type: "warning" | "error" | "collision", message, path?, collision?}. Members keep that order, optional members are omitted
// when absent, and the literal union values are the exact strings. Pi has no test of this file.
func TestPiCodingAgentSrcCoreDiagnosticsShape(t *testing.T) {
	full := ResourceDiagnostic{
		Type: DiagnosticCollision, Message: `name "a" collision`, Path: "/p/loser",
		Collision: &ResourceCollision{ResourceType: "skill", Name: "a", WinnerPath: "/p/winner", LoserPath: "/p/loser", WinnerSource: "npm:foo", LoserSource: "local"},
	}
	got, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"collision","message":"name \"a\" collision","path":"/p/loser","collision":{"resourceType":"skill","name":"a","winnerPath":"/p/winner","loserPath":"/p/loser","winnerSource":"npm:foo","loserSource":"local"}}`
	if string(got) != want {
		t.Fatalf("full diagnostic = %s, want %s", got, want)
	}
	minimal, err := json.Marshal(ResourceDiagnostic{Type: DiagnosticWarning, Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if string(minimal) != `{"type":"warning","message":"m"}` {
		t.Fatalf("minimal diagnostic = %s, want the optional path and collision omitted", minimal)
	}
	for value, want := range map[string]string{DiagnosticWarning: "warning", DiagnosticError: "error", DiagnosticCollision: "collision"} {
		if value != want {
			t.Fatalf("diagnostic type constant %q, want %q", value, want)
		}
	}
	bare, err := json.Marshal(ResourceCollision{ResourceType: "theme", Name: "n", WinnerPath: "w", LoserPath: "l"})
	if err != nil || string(bare) != `{"resourceType":"theme","name":"n","winnerPath":"w","loserPath":"l"}` {
		t.Fatalf("collision without sources = %s (%v)", bare, err)
	}
}
