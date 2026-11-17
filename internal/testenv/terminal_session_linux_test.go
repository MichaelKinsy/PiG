//go:build linux

package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
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

// The child has written its whole report and exited before the first read of it: the helper still returns what the child wrote. The helper once closed the
// terminal before the copy had read anything, and a child that passed left an empty body (a test that did not run in a terminal session).
func TestInTerminalSessionKeepsWhatTheChildWroteWhenTheCopyStartsLate(t *testing.T) {
	terminalSessionCopyDelay = 500 * time.Millisecond
	t.Cleanup(func() { terminalSessionCopyDelay = 0 })
	if !InTerminalSession(t) {
		return
	}
}

// strayHolderEnv names the file in which the terminal-session child of the test below records the process it leaves holding the terminal.
const strayHolderEnv = "PIG_TEST_STRAY_TERMINAL_HOLDER"

// A process the child started in a session of its own keeps the terminal open after the child has gone. The helper does not wait for it past the test's
// budget and does not pass: it fails and shows what the child wrote. The test runs the failing helper in a child test binary and reads its verdict.
func TestInTerminalSessionFailsWithWhatWasReadWhenAProcessOutsideItKeepsTheTerminal(t *testing.T) {
	pidFile := os.Getenv(strayHolderEnv)
	if pidFile == "" {
		runStrayHolderCase(t)
		return
	}
	if !InTerminalSession(t) {
		return
	}
	// In the terminal session: say something, leave a holder of the terminal in another session, and end the test.
	fmt.Println("the child wrote this before it left")
	holder := exec.Command("sleep", "30")
	holder.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(holder.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runStrayHolderCase(t *testing.T) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "holder.pid")
	t.Cleanup(func() {
		if text, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(string(text)); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(testbudget.DeadlineContext(t), self, "-test.run=^"+t.Name()+"$", "-test.count=1", "-test.v")
	command.Env = append(os.Environ(), strayHolderEnv+"="+pidFile, testbudget.Env+"=5")
	output, err := command.CombinedOutput()
	text := string(output)
	if err == nil {
		t.Fatalf("the helper passed a test whose child left a process holding the terminal:\n%s", text)
	}
	for _, want := range []string{"--- FAIL: " + t.Name(), "still holds the terminal", "the child wrote this before it left"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the helper's failure lacks %q (exit: %v):\n%s", want, err, text)
		}
	}
}
