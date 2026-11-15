package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyRulesRejectsRoadmapCommentsAndUserVisiblePromises(t *testing.T) {
	commentCases := map[string]string{
		"// Phase 3.1 wires the selector":                  "SH001",
		"// trust enforcement lands in P1":                 "SH001",
		"// a future row will connect this implementation": "SH002",
		"// TODO: replace this placeholder":                "SH003",
		"// pig divergence: hidden behavior gap":           "SH005",
		"// pig additive from an old implementation":       "SH005",
		"// Gopi uses a separate path":                     "SH006",
	}
	for text, code := range commentCases {
		findings := applyRules("x.go", 1, text, false)
		if len(findings) == 0 || findings[0].code != code {
			t.Fatalf("comment %q findings = %+v, want %s", text, findings, code)
		}
	}
	findings := applyRules("x.go", 1, "feature is not yet implemented", true)
	if len(findings) == 0 || findings[0].code != "SH004" {
		t.Fatalf("string findings = %+v, want SH004", findings)
	}
}

func TestApplyRulesAcceptsCurrentBehaviorAndDurableCompatibility(t *testing.T) {
	for _, text := range []string{
		"// Project settings are read only after trust resolves.",
		"// Accepts the persisted Pi session header shape.",
		"// pig divergence (D57): reject an unloadable extension root.",
		"// pig additive (D18): validate the baked Piglet closure.",
		"extension protocol version mismatch",
	} {
		if findings := applyRules("x.go", 1, text, stringsAreUserVisible(text)); len(findings) != 0 {
			t.Fatalf("text %q findings = %+v", text, findings)
		}
	}
}

// A string literal is program output: its marker is the upper-case `TODO:`, and a lower-case program name before a colon is not one.
func TestApplyRulesTreatsOnlyUpperCaseTodoInStringsAsMarker(t *testing.T) {
	for _, text := range []string{`"todo:"`, `"chord-todo: failed"`} {
		if findings := applyRules("x.go", 1, text, true); len(findings) != 0 {
			t.Fatalf("string %s findings = %+v, want none", text, findings)
		}
	}
	if findings := applyRules("x.go", 1, `"TODO: later"`, true); len(findings) == 0 || findings[0].code != "SH004" {
		t.Fatalf("string TODO findings = %+v, want SH004", findings)
	}
	if findings := applyRules("x.go", 1, "// todo: x", false); len(findings) == 0 || findings[0].code != "SH003" {
		t.Fatalf("comment todo findings = %+v, want SH003", findings)
	}
}

func TestChangedLinesAcceptsLargeGeneratedDiffLines(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "PiG Test"},
		{"config", "user.email", "pig-test@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	path := filepath.Join(root, "generated.go")
	if err := os.WriteFile(path, []byte("package generated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "generated.go"}, {"commit", "--quiet", "-m", "baseline"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	content := "package generated\n\nconst data = `" + strings.Repeat("x", 128*1024) + "`\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	lines, err := changedLines(root, "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lines["generated.go"][3]; !ok {
		t.Fatalf("changed lines = %#v, want generated.go line 3", lines)
	}
}

func stringsAreUserVisible(text string) bool { return text[0] != '/' }
