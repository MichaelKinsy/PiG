package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi resource-loader.ts:502-504 passes additionalThemePaths through mergePaths even when a path is missing; loadThemes then diagnoses it at :886-890.
func TestCollectThemePathsRetainsMissingCLIPathForDiagnostics(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	missing := filepath.Join(cwd, "missing.json")
	got := collectThemePaths(cwd, agentDir, sm, CLIFlags{NoThemes: true, Themes: []string{missing}}, false)
	if !slices.Equal(got, []string{missing}) {
		t.Fatalf("theme paths = %q, want missing explicit path %q for diagnostics", got, missing)
	}
}
