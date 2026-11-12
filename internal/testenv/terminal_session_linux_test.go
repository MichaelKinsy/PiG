//go:build linux

package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// failureRecorder is a testing.TB that records an Errorf instead of failing the test.
type failureRecorder struct {
	testing.TB
	failures []string
}

func (r *failureRecorder) Helper() {}
func (r *failureRecorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// The test body runs as PiG does in a user's terminal: it leads a session, its terminal is the controlling terminal, and its process group is the
// terminal's foreground group.
func TestInTerminalSessionRunsTheTestAsTheLeaderOfAControllingTerminal(t *testing.T) {
	if !InTerminalSession(t) {
		return
	}
	if sid, err := unix.Getsid(0); err != nil || sid != os.Getpid() {
		t.Fatalf("session %d (%v), want the test process %d as the session leader", sid, err, os.Getpid())
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("the terminal is not the controlling terminal: %v", err)
	}
	_ = tty.Close()
	var recorder failureRecorder
	RequireTerminalForeground(&recorder)
	if len(recorder.failures) != 0 {
		t.Fatalf("a fresh terminal session reports %v", recorder.failures)
	}
}

// A process that makes its own group the terminal's foreground group fails RequireTerminalForeground, and giving the terminal back passes it again.
func TestRequireTerminalForegroundFailsWhileAnotherGroupHoldsTheTerminal(t *testing.T) {
	if !InTerminalSession(t) {
		return
	}
	other := exec.Command("sleep", "600")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	// tcsetpgrp from the foreground group is allowed; a background group raises SIGTTOU unless it is ignored, which the test restores.
	signal.Ignore(syscall.SIGTTOU)
	t.Cleanup(func() { signal.Reset(syscall.SIGTTOU) })
	if err := unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, other.Process.Pid); err != nil {
		t.Fatal(err)
	}
	var held failureRecorder
	RequireTerminalForeground(&held)
	if len(held.failures) != 1 {
		t.Fatalf("with another group in the foreground RequireTerminalForeground reported %v, want one failure", held.failures)
	}
	if err := unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, syscall.Getpgrp()); err != nil {
		t.Fatal(err)
	}
	var returned failureRecorder
	RequireTerminalForeground(&returned)
	if len(returned.failures) != 0 {
		t.Fatalf("with the terminal given back RequireTerminalForeground reported %v", returned.failures)
	}
}
