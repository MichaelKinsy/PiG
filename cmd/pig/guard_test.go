package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBinaryNamedPiIsRejected(t *testing.T) {
	built := buildPigBinaryForSignalTest(t)
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	// Windows starts only pi.exe, which the guard rejects like pi.
	binary := testExecutable(filepath.Join(t.TempDir(), "pi"))
	if err := os.WriteFile(binary, data, 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command(binary, "--version").CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %v, want process exit; output: %s", err, output)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit = %d, want 2; output: %s", exitErr.ExitCode(), output)
	}
	if !strings.Contains(string(output), `refusing to run as "`+filepath.Base(binary)+`"`) {
		t.Fatalf("output = %q", output)
	}
}
