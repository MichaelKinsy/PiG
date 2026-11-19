//go:build unix

package codingagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const foregroundDeadChildEnv = "PIG_FOREGROUND_DEAD_CHILD"

// TestForegroundDeadTerminalChild runs the SIGCONT foreground check against a probe that fails as a hung-up terminal does, in a child process
// because the dead-terminal exit ends the process.
func TestForegroundDeadTerminalChild(t *testing.T) {
	scenario := os.Getenv(foregroundDeadChildEnv)
	if scenario == "" {
		t.Skip("child process only")
	}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR"), AppVersion: "9.9.9"})
	switch scenario {
	case "eio":
		_, _ = m.foregroundOrExit(func() (bool, error) { return false, &os.PathError{Op: "ioctl", Path: "/dev/pts/0", Err: syscall.EIO} })
	case "other":
		_, _ = m.foregroundOrExit(func() (bool, error) { return false, syscall.ECONNREFUSED })
	}
	_, _ = os.Stdout.WriteString("survived\n")
	os.Exit(0)
}

// A suspended session whose foreground check fails with EIO, the answer of a hung-up terminal, takes the dead-terminal exit (129) and records
// it, as Pi's setRawMode EIO reaches emergencyTerminalExit (D227). An error that is not a dead terminal does not exit.
func TestForegroundCheckFailingWithEIOTakesTheDeadTerminalExit(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		exit     int
	}{{"eio", 129}, {"other", 0}} {
		t.Run(tc.scenario, func(t *testing.T) {
			agentDir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestForegroundDeadTerminalChild$")
			cmd.Env = append(os.Environ(), foregroundDeadChildEnv+"="+tc.scenario, "PIG_DEAD_TERMINAL_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
			output, err := cmd.CombinedOutput()
			code := 0
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.exit {
				t.Fatalf("exit code %d, want %d\n%s", code, tc.exit, output)
			}
			log, _ := os.ReadFile(agentDir + "/exit.log")
			if tc.exit == 129 && (!strings.Contains(string(log), "input/output error") || !strings.Contains(string(log), "(exit 129)") || strings.Contains(string(output), "survived")) {
				t.Fatalf("exit.log %q, output %q: want the terminal-gone reason with exit 129", log, output)
			}
		})
	}
}

const foregroundNotControllingChildEnv = "PIG_FOREGROUND_NOT_CONTROLLING_CHILD"

// TestForegroundNotControllingTerminalChild runs the production foreground check (tui.StdinInForeground, through foregroundOrExit) on a stdin that is a
// terminal but not this process's controlling terminal, in a child process because a dead-terminal verdict ends the process. Only the terminal
// operations are recorded; the check is the real one.
func TestForegroundNotControllingTerminalChild(t *testing.T) {
	if os.Getenv(foregroundNotControllingChildEnv) == "" {
		t.Skip("child process only")
	}
	if _, err := unix.IoctlGetInt(0, unix.TIOCGPGRP); !errors.Is(err, unix.ENOTTY) {
		t.Fatalf("premise: tcgetpgrp(stdin) = %v, want ENOTTY (stdin must be a terminal that is not the controlling terminal)", err)
	}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR"), AppVersion: "9.9.9"})
	var calls []string
	var onContinue func() error
	ops := m.suspendOperations()
	ops.keepAlive = func(time.Duration) func() { return func() {} }
	ops.ignoreInterrupt = func() func() { return func() {} }
	ops.continued = func(continued func() error, _ func()) func() { onContinue = continued; return func() {} }
	ops.stop = func() {}
	ops.start = func() error { calls = append(calls, "start"); return nil }
	ops.requestRender = func() { calls = append(calls, "render-force") }
	ops.kill = func(pid int) error { calls = append(calls, fmt.Sprintf("kill:%d:SIGTSTP", pid)); return nil }
	if err := suspendTerminal(t.Context(), "linux", nil, ops); err != nil {
		t.Fatal(err)
	}
	calls = calls[:0] // the suspend's own SIGTSTP
	err := onContinue()
	_, _ = fmt.Fprintf(os.Stdout, "resume err=%v calls=%q\n", err, calls)
	os.Exit(0)
}

// A SIGCONT that reaches a session whose stdin is a terminal that is not the controlling terminal restarts the UI as Pi's handleCtrlZ does
// (ui.start(), then ui.requestRender(true)): tcgetpgrp answers ENOTTY there, job control does not apply, so the check neither stops the group
// again nor takes the dead-terminal exit (129) that ENOTTY from a terminal write or setRawMode takes (D227).
func TestResumeWithoutAControllingTerminalRestartsTheUI(t *testing.T) {
	master, slave := openTestPTY(t) // O_NOCTTY: the child does not get it as controlling terminal
	_ = master
	agentDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestForegroundNotControllingTerminalChild$")
	cmd.Env = append(os.Environ(), foregroundNotControllingChildEnv+"=1", "PIG_DEAD_TERMINAL_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
	cmd.Stdin = slave
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, output)
	}
	if want := `resume err=<nil> calls=["start" "render-force"]`; !strings.Contains(string(output), want) {
		t.Fatalf("child output %q, want %q: raw mode entered and a full render, no SIGTSTP, no exit", output, want)
	}
}
