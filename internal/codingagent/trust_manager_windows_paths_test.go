package codingagent

// pi: packages/coding-agent/src/core/trust-manager.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/coding-agent/test/trust-manager.test.ts "ignores the user .agents/skills dir when HOME differs only by case on Windows" (Pi 1be312b4, #10488).
func TestHasTrustRequiringProjectResourcesIgnoresTheUserSkillsDirWhenHomeDiffersOnlyByCaseOnWindows(t *testing.T) {
	// Resolve the temp root first so a symlinked temp root with upper-case letters (e.g. /tmp/L.7 -> /tmp/R.x)
	// does not turn the lower-cased path into a nonexistent path on a case-sensitive Linux FS; on real Windows
	// the FS is case-insensitive so realpath resolves any casing.
	base := testenv.EvalSymlinks(t, t.TempDir())
	mixedCaseHome := filepath.Join(base, "Home")
	project := filepath.Join(mixedCaseHome, "project")
	if err := os.MkdirAll(filepath.Join(mixedCaseHome, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if HasTrustRequiringProjectResources(project) {
		t.Fatal("the user's .agents/skills counted as a project resource because HOME differs from the working directory in casing")
	}
}

// "matches saved decisions regardless of casing on Windows".
func TestProjectTrustStoreMatchesSavedDecisionsRegardlessOfCasingOnWindows(t *testing.T) {
	agentDir := t.TempDir()
	base := testenv.EvalSymlinks(t, t.TempDir())
	project := filepath.Join(base, "Project-Mixed")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewProjectTrustStore(agentDir)
	if err := store.Set(project, new(true)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	lower := filepath.Join(base, "project-mixed")
	if got, err := store.Get(lower); err != nil || got == nil || !*got {
		t.Fatalf("Get(%q) = %v, %v; want the saved true", lower, got, err)
	}
	if err := store.Set(lower, new(false)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]bool
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	values := make([]bool, 0, len(saved))
	for _, value := range saved {
		values = append(values, value)
	}
	if !slices.Equal(values, []bool{false}) {
		t.Fatalf("trust.json = %v, want exactly the one replaced decision false", saved)
	}
	if got, err := store.Get(project); err != nil || got == nil || *got {
		t.Fatalf("Get(%q) = %v, %v; want the replaced false", project, got, err)
	}
}
