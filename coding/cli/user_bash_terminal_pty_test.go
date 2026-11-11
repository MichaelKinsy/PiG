//go:build linux

package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's shell is detached (core/tools/bash.ts createLocalShellOperations spawns with `detached: true`, a new session with no controlling terminal), so a
// command cannot take PiG's terminal. PiG started the shell in its own process group but in PiG's session: an interactive job-control shell
// (`zsh -f -i -c true; true`) opened /dev/tty, made its own process group the terminal's foreground group and left it so, and PiG's next read from the
// terminal raised SIGTTIN: the user's shell reported `suspended (tty input)` (issue #427).
//
// This test runs the complete user path: PiG's TUI on its own terminal runs a user bash command (`!cmd`, which shares the local shell operations of the
// bash tool), the command does what zsh -i does with the terminal, and the user presses a key.
func TestInteractiveUserBashLeavesTheTerminalToPig(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	grabber, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, cwd := shortTempDir(t), t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"theme":"dark","tuiMode":"regular"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t, 40, 400)
	t.Cleanup(func() { _ = master.Close() })
	cmd := exec.Command(binary, "--model", "test-faux/faux-1", "--no-session", "--no-extensions")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
		"PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		// A stopped process ignores SIGKILL until it continues.
		_ = cmd.Process.Signal(syscall.SIGCONT)
		_ = cmd.Process.Kill()
		<-exited
	})
	output := &ptyOutput{}
	go func() { _, _ = io.Copy(output, master) }()

	output.waitQuiet(0, []byte("faux-1"), 300*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(output.since(0), []byte("faux-1")) {
		t.Fatalf("the first frame never drew the footer; output tail: %q", tail(output.since(0), 1500))
	}

	// The command stands in for `zsh -i`; the echo after it shows the command ended. The test binary named by the command runs TestMain's stand-in
	// (testenv.GrabTerminalIfRequested), the shell forks it because the echo follows, and the echoed marker is not in the typed text.
	typedCommand := "!" + testenv.TerminalGrabberHelper + "=1 '" + strings.ReplaceAll(grabber, "'", `'\''`) + "'; echo done-$((6*7))"
	mark := output.mark()
	if _, err := master.WriteString(typedCommand); err != nil {
		t.Fatal(err)
	}
	output.waitQuiet(mark, []byte("6*7))"), 200*time.Millisecond, testbudget.Wait(t))
	if _, err := master.WriteString("\r"); err != nil {
		t.Fatal(err)
	}
	output.waitQuiet(mark, []byte("done-42"), 200*time.Millisecond, testbudget.Wait(t))
	text := string(tools.StripANSI(output.since(mark)))
	if !strings.Contains(text, "done-42") {
		t.Fatalf("the user bash command never finished; output tail: %q", tail([]byte(text), 1500))
	}
	if !strings.Contains(text, testenv.NoControllingTerminal) {
		t.Errorf("the command could open /dev/tty: Pi's shell has no controlling terminal; output tail: %q", tail([]byte(text), 600))
	}
	stat := readProcStat(t, cmd.Process.Pid)
	if stat.foregroundGroup != stat.group {
		t.Fatalf("after the command the terminal's foreground group is %d, not PiG's group %d: PiG's next read from the terminal raises SIGTTIN", stat.foregroundGroup, stat.group)
	}

	typed := []byte("terminal-key-probe")
	mark = output.mark()
	if _, err := master.Write(typed); err != nil {
		t.Fatal(err)
	}
	output.waitQuiet(mark, typed, 100*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(output.since(mark), typed) {
		t.Errorf("the key presses after the command never reached the editor; PiG's state %c; output tail: %q", readProcStat(t, cmd.Process.Pid).state, tail(output.since(mark), 600))
	}
	if state := readProcStat(t, cmd.Process.Pid).state; state == 'T' {
		t.Errorf("PiG stopped (state T) after a key press, as a SIGTTIN does")
	}
}

// procStat is the part of /proc/<pid>/stat these tests read.
type procStat struct {
	state rune
	// group is the process group of the process, foregroundGroup the foreground group of its terminal.
	group, foregroundGroup int
}

func readProcStat(t *testing.T, pid int) procStat {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	// The command name is in parentheses and may hold spaces; the fields after it are: state ppid pgrp session tty_nr tpgid.
	fields := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
	if len(fields) < 6 {
		t.Fatalf("short /proc/%d/stat: %q", pid, data)
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		t.Fatal(err)
	}
	foreground, err := strconv.Atoi(fields[5])
	if err != nil {
		t.Fatal(err)
	}
	return procStat{state: rune(fields[0][0]), group: group, foregroundGroup: foreground}
}
