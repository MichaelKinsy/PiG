//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// /usr/bin/env resolves its program with the child's PATH, so the Windows
// emulation must use the scenario's effective PATH, not the runner's.
func TestEnvLauncherProgramUsesScenarioPath(t *testing.T) {
	dir := t.TempDir()
	file := "scenario-tool"
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	environ := []string{"PATH=" + t.TempDir(), "Path=" + dir}
	got, err := envLauncherProgram(t, "scenario-tool", effectivePath(environ))
	if err != nil {
		t.Fatalf("scenario PATH lookup: %v", err)
	}
	if filepath.Dir(got) != dir {
		t.Fatalf("resolved %q, want a program in %q", got, dir)
	}
	if _, err := envLauncherProgram(t, "scenario-tool", t.TempDir()); err == nil {
		t.Fatal("a program outside the scenario PATH must not resolve")
	}
}
