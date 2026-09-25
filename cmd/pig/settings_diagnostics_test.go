package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrintModeReportsInvalidSettingsOnce drives the real binary: upstream
// main.ts collects settings diagnostics from the startup and runtime settings
// managers, deduplicates them, and reports them on stderr before a
// non-interactive run. Both managers read the same broken file, so the warning
// must appear exactly once.
func TestPrintModeReportsInvalidSettingsOnce(t *testing.T) {
	bin := buildPigBinaryForDiagnosticsTest(t)
	agentDir := t.TempDir()
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "--no-session", "--print", "TUI_LIVE_STREAM")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"PIG_HOME="+t.TempDir(),
		"PIG_CODING_AGENT_DIR="+agentDir,
		"PIG_TEST_FAUX=1",
		"PIG_TEST_FAUX_SCENARIO=parity-basic",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig --print: %v\nstderr:\n%s", err, stderr.String())
	}
	want := "Warning: Invalid settings file " + settingsPath + ": "
	if got := strings.Count(stderr.String(), want); got != 1 {
		t.Fatalf("stderr has %d copies of %q, want 1:\n%s", got, want, stderr.String())
	}
}

func buildPigBinaryForDiagnosticsTest(t *testing.T) string {
	t.Helper()
	out := testExecutable(filepath.Join(t.TempDir(), "pig-diagnostics-test"))
	if data, err := exec.Command("go", "build", "-o", out, ".").CombinedOutput(); err != nil {
		t.Fatalf("build pig: %v\n%s", err, data)
	}
	return out
}
