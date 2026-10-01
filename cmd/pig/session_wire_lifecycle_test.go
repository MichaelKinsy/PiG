//go:build linux

package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The readiness regression holds command completion after the model footer appears and requires one submission only after acknowledgement.
func TestModeWireReadiness(t *testing.T) {
	script, err := filepath.Abs("../../test/parity/testdata/session-wire-readiness-test.py")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "python3", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("readiness: %v\n%s", err, output)
	}
}

// The probe drives the real provider-request and command-completion barriers.
// Pi 0.87.1 print-mode.ts:49-64 leaves SIGINT unhandled. Interactive-mode.ts:
// 4145-4157,4179-4185 exits 0 on live SIGHUP and 129 on a dead terminal.
func TestModeWireLifecycle(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	script, err := filepath.Abs("../../test/parity/testdata/session-wire.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"print", "json", "disconnect", "live-hup", "modal-shutdown"} {
		t.Run(name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "python3", script, "--bin", binary, "--case", name)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("lifecycle: %v\n%s", err, output)
			}
		})
	}
}
