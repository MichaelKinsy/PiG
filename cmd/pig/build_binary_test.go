package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// testExecutable returns path as a name the platform can start: Windows runs
// only files with an executable extension.
func testExecutable(path string) string {
	if runtime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

func buildPigBinaryForSignalTest(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pig-signal-test")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "."
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build pig: %v\n%s", err, data)
	}
	return out
}
