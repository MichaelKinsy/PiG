//go:build unix

package codingagent

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
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
