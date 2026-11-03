//go:build linux || darwin

package codingagent

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

const deadTerminalWriteChildEnv = "PIG_DEAD_TERMINAL_WRITE_CHILD"

// TestDeadTerminalWriteChild writes to a terminal whose master side closed, in a child process because the emergency exit ends the process.
func TestDeadTerminalWriteChild(t *testing.T) {
	if os.Getenv(deadTerminalWriteChildEnv) == "" {
		t.Skip("child process only")
	}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR"), AppVersion: "9.9.9"})
	master, slave := openTestPTY(t)
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	_ = m.exitOnDeadTerminalWrite()
	tui.NewProcessTerminal(slave, slave).Write("frame")
	_, _ = os.Stdout.WriteString("survived\n")
	os.Exit(0)
}

// Pi's stdout "error" listener turns a write that fails with EIO on a hung-up terminal into emergencyTerminalExit: exit 129 with no crash report.
// PiG's terminal write used to discard the error, so a session whose terminal was gone kept running.
func TestWriteToAHungUpTerminalExits129(t *testing.T) {
	agentDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeadTerminalWriteChild$")
	cmd.Env = append(os.Environ(), deadTerminalWriteChildEnv+"=1", "PIG_DEAD_TERMINAL_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 129 || strings.Contains(string(output), "survived") {
		t.Fatalf("exit code %d, want 129 without carrying on\n%s", code, output)
	}
	log, _ := os.ReadFile(agentDir + "/exit.log")
	if !strings.Contains(string(log), "terminal gone: write /dev/") || !strings.Contains(string(log), "input/output error (exit 129)") {
		t.Fatalf("exit.log = %q, want the terminal-gone reason with exit 129", log)
	}
	if n := len(ReadCrashLog(CrashLogPath(agentDir))); n != 0 {
		t.Errorf("crash records = %d, want 0", n)
	}
}

const deadRemoteEditorWriteChildEnv = "PIG_DEAD_REMOTE_EDITOR_WRITE_CHILD"

// TestDeadRemoteEditorWriteChild is a remote editor component's terminal.write against a terminal whose master side closed, through the
// path named by the env var: "stopped" (the TUI already stopped, TerminalWrite writes to stdout) or "between-frames" (writeTerminal with a
// TUI that has no raw writer), "set-title" (ExtUIContext.SetTitle) or "osc52" (the clipboard copier).
func TestDeadRemoteEditorWriteChild(t *testing.T) {
	path := os.Getenv(deadRemoteEditorWriteChildEnv)
	if path == "" {
		t.Skip("child process only")
	}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR"), AppVersion: "9.9.9"})
	master, slave := openTestPTY(t)
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	_ = m.exitOnDeadTerminalWrite()
	stdout := os.Stdout
	os.Stdout = slave
	switch path {
	case "stopped":
		m.tuiStopped.Store(true)
		(&remoteEditor{m: m}).TerminalWrite("data")
	case "between-frames":
		m.writeTerminal("data")
	case "set-title":
		m.tuiInst = tui.NewTuiMainScreen(tui.NewProcessTerminal(slave, slave), nil, "")
		(&ExtUIContext{m: m}).SetTitle("title")
	case "osc52":
		hostClipboardCopier().emitOSC52("text")
	}
	_, _ = stdout.WriteString("survived\n")
	os.Exit(0)
}

// In Pi an extension component's tui.terminal.write is process.stdout.write, so a write to a hung-up terminal reaches the stdout "error" listener
// and ends in emergencyTerminalExit (exit 129), wherever the write comes from.
func TestRemoteEditorTerminalWriteToAHungUpTerminalExits129(t *testing.T) {
	for _, path := range []string{"stopped", "between-frames", "set-title", "osc52"} {
		t.Run(path, func(t *testing.T) {
			agentDir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestDeadRemoteEditorWriteChild$")
			cmd.Env = append(os.Environ(), deadRemoteEditorWriteChildEnv+"="+path, "PIG_DEAD_TERMINAL_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
			output, err := cmd.CombinedOutput()
			code := 0
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != 129 || strings.Contains(string(output), "survived") {
				t.Fatalf("exit code %d, want 129 without carrying on\n%s", code, output)
			}
		})
	}
}

const otherTerminalWriteChildEnv = "PIG_OTHER_TERMINAL_WRITE_CHILD"

type failingTerminalWriter struct{ err error }

func (w failingTerminalWriter) Write([]byte) (int, error) { return 0, w.err }

// TestOtherTerminalWriteChild writes to a terminal whose write fails with the errno in the env var.
func TestOtherTerminalWriteChild(t *testing.T) {
	name := os.Getenv(otherTerminalWriteChildEnv)
	if name == "" {
		t.Skip("child process only")
	}
	errno := map[string]syscall.Errno{"EINVAL": syscall.EINVAL, "EIO": syscall.EIO}[name]
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR"), AppVersion: "9.9.9"})
	_ = m.exitOnDeadTerminalWrite()
	tui.NewProcessTerminalWithOutput(nil, nil, failingTerminalWriter{&os.PathError{Op: "write", Path: "/dev/pts/9", Err: errno}}).Write("frame")
	_, _ = os.Stdout.WriteString("survived\n")
	os.Exit(0)
}

// Pi's terminalErrorHandler exits through emergencyTerminalExit only for DEAD_TERMINAL_ERROR_CODES and rethrows every other error
// (interactive-mode.ts:4510), which uncaughtCrash turns into a crash record and exit 1 (:4448). EIO is the dead terminal (129, no record).
func TestTerminalWriteErrorOutsideTheDeadTerminalCodesCrashesLikePi(t *testing.T) {
	for _, tc := range []struct {
		errno       string
		code        int
		crashCount  int
		wantSurvive bool
	}{
		{"EINVAL", 1, 1, false},
		{"EIO", 129, 0, false},
	} {
		t.Run(tc.errno, func(t *testing.T) {
			agentDir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestOtherTerminalWriteChild$")
			cmd.Env = append(os.Environ(), otherTerminalWriteChildEnv+"="+tc.errno, "PIG_DEAD_TERMINAL_AGENT_DIR="+agentDir, "PIG_HOME="+t.TempDir())
			output, err := cmd.CombinedOutput()
			code := 0
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.code || strings.Contains(string(output), "survived") {
				t.Fatalf("exit code %d, want %d without carrying on\n%s", code, tc.code, output)
			}
			if n := len(ReadCrashLog(CrashLogPath(agentDir))); n != tc.crashCount {
				t.Errorf("crash records = %d, want %d\n%s", n, tc.crashCount, output)
			}
		})
	}
}
