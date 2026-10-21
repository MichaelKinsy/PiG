//go:build linux

package cli

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The readiness regression holds command completion after the model footer appears and requires one submission only after acknowledgement.
// It also requires the disconnect probe's extension to keep the session writing, so the hangup always meets a write (an idle Pi exits 0 at EOF).
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

// The probe drives the real provider-request and command-completion barriers. It carries no deadline of its own, because
// how long pig takes to start and to shut down is its CPU time divided by the share of a CPU the host gives it; the
// bound on a hang is the test budget, and the probe answers its SIGTERM with the barrier it was waiting for and the
// terminal output it had read.
// Pi 0.87.1 print-mode.ts:49-64 leaves SIGINT unhandled. Interactive-mode.ts:
// 4145-4157,4179-4185 exits 0 on live SIGHUP and 129 on a dead terminal.
func TestModeWireLifecycle(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	script, err := filepath.Abs("../../test/parity/testdata/session-wire.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"print", "json", "disconnect", "disconnect-repo", "live-hup", "modal-shutdown"} {
		t.Run(name, func(t *testing.T) {
			command := exec.CommandContext(testbudget.Context(t), "python3", script, "--bin", binary, "--case", name)
			command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
			command.WaitDelay = 10 * time.Second
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("lifecycle: %v\n%s", err, output)
			}
		})
	}
}
