//go:build linux

package codingagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Input that ended because the terminal hung up, on the terminal output also goes to, is a dead terminal: the process
// exits 129 as Pi does on that terminal's EIO, although the terminal can still answer TIOCGWINSZ while the hang-up
// completes. The size probe alone raced the hang-up and kept the session running without input (parity
// 10-terminal-disconnect-exit timed out on a loaded host). Input that ended while output goes to a live terminal keeps
// the session running (#165, TestInteractiveKeepsRunningWhenInputEndsOnALiveTerminal).
func TestHungUpTerminalEOFExits129(t *testing.T) {
	if os.Getenv(deadTerminalChildEnv) == "hung-up-eof" {
		m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR"), AppVersion: "9.9.9"})
		m.exitIfDeadTerminal(io.EOF)
		fmt.Fprintln(os.Stderr, "survived")
		os.Exit(0)
	}
	run := func(t *testing.T, stdin, stdout *os.File) (int, string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestHungUpTerminalEOFExits129$")
		cmd.Env = append(os.Environ(), deadTerminalChildEnv+"=hung-up-eof", "PIG_DEAD_TERMINAL_AGENT_DIR="+t.TempDir(), "PIG_HOME="+t.TempDir())
		cmd.Stdin, cmd.Stdout = stdin, stdout
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return exit.ExitCode(), stderr.String()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0, stderr.String()
	}
	t.Run("input and output on the hung-up terminal", func(t *testing.T) {
		master, slave := openTestPTY(t)
		if err := master.Close(); err != nil {
			t.Fatal(err)
		}
		if code, stderr := run(t, slave, slave); code != 129 {
			t.Fatalf("exit %d, want 129\n%s", code, stderr)
		}
	})
	t.Run("output on a live terminal", func(t *testing.T) {
		hungMaster, hungSlave := openTestPTY(t)
		_, liveSlave := openTestPTY(t)
		if err := hungMaster.Close(); err != nil {
			t.Fatal(err)
		}
		if code, stderr := run(t, hungSlave, liveSlave); code != 0 || !strings.Contains(stderr, "survived") {
			t.Fatalf("exit %d, want the session to keep running\n%s", code, stderr)
		}
	})
}
