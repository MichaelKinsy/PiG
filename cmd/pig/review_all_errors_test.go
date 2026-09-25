package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Upstream main.ts reports every entry of resourceLoader.getExtensions().errors
// in one run: a Package extension failure and a missing -e path are both listed
// before the hint and exit 1.
func TestReviewStartupReportsPackageAndCLIExtensionFailuresTogether(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "cwd")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := writeStartupPackages(t, filepath.Join(home, "packages"), false)
	settings, err := json.Marshal(map[string]any{"packages": []string{f.mixed, f.healthy}})
	if err != nil {
		t.Fatal(err)
	}
	writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), string(settings))
	missing := filepath.Join(home, "missing-ext")
	binary := buildPigBinaryForSignalTest(t)

	failed := runRPCStartup(t, binary, home, agentDir, cwd, "-e", missing)
	var exitErr *exec.ExitError
	if !errors.As(failed.err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("RPC startup error = %v, want exit 1\nstderr:\n%s", failed.err, failed.stderr)
	}
	for _, want := range []string{
		`Failed to load extension "` + f.badExtension + `"`,
		`Failed to load extension "` + missing + `": Extension path does not exist: ` + missing,
	} {
		if !strings.Contains(failed.stderr, want) {
			t.Errorf("stderr lacks %q\nstderr:\n%s", want, failed.stderr)
		}
	}
}
