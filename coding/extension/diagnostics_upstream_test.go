package extension_test

// pi: packages/coding-agent/src/core/diagnostics.ts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The union literals of ResourceDiagnostic.type (diagnostics.ts:12) and ResourceCollision.resourceType values Pi's loaders write (skills.ts:444 "skill").
func TestResourceDiagnosticTypeLiteralsMatchPisUnion(t *testing.T) {
	for got, want := range map[string]string{extension.DiagnosticWarning: "warning", extension.DiagnosticError: "error", extension.DiagnosticCollision: "collision"} {
		if got != want {
			t.Errorf("diagnostic type constant %q, want %q", got, want)
		}
	}
}

// ResourceDiagnostic and ResourceCollision keep Pi's wire names and key order, and omit their optional members (diagnostics.ts:1-16). The expected strings are
// JSON.stringify of the object literals Pi builds: the skill collision of skills.ts:439-448, the extension-shortcut warning of runner.ts:679, and a collision carrying
// both optional sources (diagnostics.ts:6-7, checked with Node 24 JSON.stringify).
func TestResourceDiagnosticWireMatchesPisObjectLiterals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		diagnostic extension.ResourceDiagnostic
		want       string
	}{
		{"skill collision", extension.ResourceDiagnostic{
			Type: extension.DiagnosticCollision, Message: `name "x" collision`, Path: "/b/SKILL.md",
			Collision: &extension.ResourceCollision{ResourceType: "skill", Name: "x", WinnerPath: "/a/SKILL.md", LoserPath: "/b/SKILL.md"},
		}, `{"type":"collision","message":"name \"x\" collision","path":"/b/SKILL.md","collision":{"resourceType":"skill","name":"x","winnerPath":"/a/SKILL.md","loserPath":"/b/SKILL.md"}}`},
		{"warning without a collision", extension.ResourceDiagnostic{
			Type: extension.DiagnosticWarning, Message: "Extension shortcut 'ctrl+c' from /ext/a conflicts with built-in shortcut. Skipping.", Path: "/ext/a",
		}, `{"type":"warning","message":"Extension shortcut 'ctrl+c' from /ext/a conflicts with built-in shortcut. Skipping.","path":"/ext/a"}`},
		{"collision with both sources and no path", extension.ResourceDiagnostic{
			Type: extension.DiagnosticCollision, Message: "m",
			Collision: &extension.ResourceCollision{ResourceType: "extension", Name: "n", WinnerPath: "/a", LoserPath: "/b", WinnerSource: "npm:foo", LoserSource: "local"},
		}, `{"type":"collision","message":"m","collision":{"resourceType":"extension","name":"n","winnerPath":"/a","loserPath":"/b","winnerSource":"npm:foo","loserSource":"local"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.diagnostic)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("wire\n got %s\nwant %s", got, tc.want)
			}
			var back extension.ResourceDiagnostic
			if err := json.Unmarshal([]byte(tc.want), &back); err != nil {
				t.Fatal(err)
			}
			again, _ := json.Marshal(back)
			if string(again) != tc.want {
				t.Errorf("round trip\n got %s\nwant %s", again, tc.want)
			}
		})
	}
}

// The runner reports a conflicting extension shortcut as a diagnostic of type "warning" with the extension's path and no collision (runner.ts:678-679,
// extensions-runner.test.ts:170 "warns when extension shortcut conflicts with built-in").
func TestRunnerShortcutConflictIsAWarningDiagnosticWithThePath(t *testing.T) {
	ext := extension.Extension{
		Path: "/ext/a", ResolvedPath: "/ext/a",
		Handlers: map[string][]extension.HandlerFn{}, Tools: map[string]extension.RegisteredTool{}, MessageRenderers: map[string]extension.MessageRenderer{},
		Commands: map[string]extension.RegisteredCommand{}, Flags: map[string]extension.ExtensionFlag{},
		Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{"ctrl+c": {
			Shortcut: "ctrl+c", Description: "Conflicts with built-in", Handler: func(context.Context) error { return nil }, ExtensionPath: "/ext/a",
		}},
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, ".")
	if got := runner.Shortcuts(map[string][]string{"app.clear": {"ctrl+c"}}); len(got) != 0 {
		t.Fatalf("a reserved shortcut was registered: %v", got)
	}
	diagnostics := runner.ShortcutDiagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one", diagnostics)
	}
	d := diagnostics[0]
	if d.Type != extension.DiagnosticWarning || d.Path != "/ext/a" || d.Collision != nil {
		t.Errorf("diagnostic = %#v, want a warning for /ext/a without a collision", d)
	}
	got, _ := json.Marshal(d)
	if want := `{"type":"warning","message":"Extension shortcut 'ctrl+c' from /ext/a conflicts with built-in shortcut. Skipping.","path":"/ext/a"}`; string(got) != want {
		t.Errorf("wire\n got %s\nwant %s", got, want)
	}
}
