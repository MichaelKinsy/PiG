package codingagent

// pi: packages/coding-agent/src/core/trust-manager.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Ports packages/coding-agent/test/trust-manager.test.ts "ignores the user .agents/skills dir when HOME differs only by case on Windows" (Pi 1be312b4, #10488).
func TestHasTrustRequiringProjectResourcesIgnoresTheUserSkillsDirWhenHomeDiffersOnlyByCaseOnWindows(t *testing.T) {
	mixedCaseHome := filepath.Join(t.TempDir(), "Home")
	project := filepath.Join(mixedCaseHome, "project")
	if err := os.MkdirAll(filepath.Join(mixedCaseHome, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", strings.ToLower(mixedCaseHome))
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if HasTrustRequiringProjectResources(project) {
		t.Fatal("the user's .agents/skills counted as a project resource because HOME differs from the working directory in casing")
	}
}

// "matches saved decisions regardless of casing on Windows".
func TestProjectTrustStoreMatchesSavedDecisionsRegardlessOfCasingOnWindows(t *testing.T) {
	agentDir := t.TempDir()
	project := filepath.Join(t.TempDir(), "Project-Mixed")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewProjectTrustStore(agentDir)
	if err := store.Set(project, new(true)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	lower := strings.ToLower(project)
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
