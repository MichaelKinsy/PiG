//go:build linux || darwin

package codingagent

import (
	"errors"
	"os"
	"os/exec"
	"strings"
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
	remove := m.exitOnDeadTerminalWrite()
	defer remove()
	tui.NewProcessTerminal(slave, slave).Write("frame")
	os.Stdout.WriteString("survived\n")
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
