//go:build linux

package codingagent

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A read that the terminal reader turned into EOF because the master side hung up is a dead terminal, even while the
// terminal still answers TIOCGWINSZ: the process exits 129, as Pi's stdin error handler does on the slave's EIO. The
// size probe alone raced the hang-up and kept the session running without input (parity 10-terminal-disconnect-exit
// timed out on a loaded host).
func TestHungUpTerminalEOFExits129(t *testing.T) {
	if os.Getenv(deadTerminalChildEnv) == "hung-up-eof" {
		m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: os.Getenv("PIG_DEAD_TERMINAL_AGENT_DIR"), AppVersion: "9.9.9"})
		m.exitIfDeadTerminal(io.EOF)
		os.Stderr.WriteString("survived\n")
		os.Exit(0)
	}
	// Closing a master marks its slave closed at once (a read fails with EIO and polls POLLHUP), but the hang-up that
	// makes TIOCGWINSZ fail too completes later; under load the input reader saw the first while the size probe still
	// saw a live terminal. Stdin on a hung-up terminal and stdout on a live one hold that window open.
	hungMaster, hungSlave := openTestPTY(t)
	_, liveSlave := openTestPTY(t)
	if err := hungMaster.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHungUpTerminalEOFExits129$")
	cmd.Env = append(os.Environ(), deadTerminalChildEnv+"=hung-up-eof", "PIG_DEAD_TERMINAL_AGENT_DIR="+t.TempDir(), "PIG_HOME="+t.TempDir())
	cmd.Stdin, cmd.Stdout = hungSlave, liveSlave
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 129 {
		t.Fatalf("hung-up terminal: %v, want exit 129\n%s", err, stderr.String())
	}
}
