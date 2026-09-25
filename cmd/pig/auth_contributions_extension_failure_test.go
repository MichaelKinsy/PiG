package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's package commands load extensions only to resolve project trust and turn
// each extension load error into a warning (package-manager-cli.ts:770). The
// authentication inventory likewise reports each unresolvable Package or
// top-level extension as exactly one inspection diagnostic instead of failing
// the whole inventory.
func TestAuthContributionsReportUnresolvedExtensionsOnce(t *testing.T) {
	home := t.TempDir()
	cwd, agentDir := filepath.Join(home, "work"), filepath.Join(home, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, "pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	f := writeStartupPackages(t, filepath.Join(home, "packages"), false)
	if err := codingagent.NewSettingsManager(cwd, agentDir).SetPackages([]codingagent.PackageSource{{Source: f.mixed}, {Source: f.healthy}}); err != nil {
		t.Fatal(err)
	}
	topLevel := filepath.Join(agentDir, "extensions", "broken-top")
	writeStartupFixtureFile(t, filepath.Join(topLevel, "go.mod"), "module example.com/brokentop\n\ngo 1.26\n")
	writeStartupFixtureFile(t, filepath.Join(topLevel, "extension.go"), "package brokentop\n")

	registry, err := discoverAuthContributions()
	if err != nil {
		t.Fatalf("authentication inventory failed on broken extensions: %v", err)
	}
	defer registry.close()
	counts := map[string]int{}
	for _, diagnostic := range registry.diagnostics {
		counts[diagnostic.Extension]++
	}
	if len(registry.diagnostics) != 2 || counts["bad"] != 1 || counts["broken-top"] != 1 {
		t.Fatalf("diagnostics = %#v, want the Package extension and the top-level extension once each", registry.diagnostics)
	}
}
