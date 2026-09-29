package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi main.ts:857-863 reports the startup settings diagnostics, then printHelp(extensionFlags) (args.ts:261-271,333) appends an "Extension CLI Flags:" section after the extension note. Flags follow extension load order and first registration, a repeated name keeps its last declaration, the name column pads to 30 UTF-16 units without truncation, and a flag without a description names its extension path. Expected text is pinned Pi 0.87.1 output for the same fixtures.
func TestHelpListsExtensionFlagsAfterSettingsDiagnostics(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	first, err := filepath.Abs(filepath.Join("testdata", "help-flags.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := filepath.Abs(filepath.Join("testdata", "help-flags-second.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{ "theme": `), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, append([]string{"--offline", "--no-extensions", "-e", first, "-e", second}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+filepath.Join(root, "home"), "PIG_CODING_AGENT_DIR="+agentDir, "FORCE_COLOR=0")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Errorf("%q: %v stdout=%q stderr=%q", args, err, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	section := "Extensions can register additional flags (e.g., --plan from plan-mode extension).\n" +
		"Extension CLI Flags:\n" +
		"  --plan                      Plan again\n" +
		"  --apply                     Apply the plan\n" +
		"  --target-environment-name <value>Registered by " + first + "\n" +
		"  --zeta <value>              Second extension flag\n" +
		"\n\nExamples:\n"
	warning := "Warning: Invalid settings file " + settings + ": Unexpected end of JSON input\n"

	help, stderr := run("--help")
	if !strings.Contains(help, section) {
		t.Errorf("help lacks the extension flag section %q:\n%s", section, help)
	}
	if stderr != warning {
		t.Errorf("help stderr=%q, want %q", stderr, warning)
	}
	if stdout, stderr := run("--mode", "rpc", "--help"); stdout != "" || stderr != warning+help {
		t.Errorf("rpc help stdout=%q stderr=%q, want the warning then the plain help on stderr", stdout, stderr)
	}
}
