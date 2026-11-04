//go:build integration && (linux || darwin)

package integration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// hangupRuns is how many hung-up sessions each setup of TestHangupWritesOneExitLogLine checks. The defect it pins was a race
// between the kernel's SIGHUP, the input reader's EOF and a failed terminal write, each of which recorded its own exit.log
// line: it hit every run on a loaded host, but each outcome (three lines, or two without the signal's) came up in about half
// of them, so the check repeats rather than trusting one lucky run.
const hangupRuns = 100

// hangupSetup is how pig is started on the pseudo-terminal, which decides which hangup event reaches it first.
type hangupSetup struct {
	name string
	// argv is the command run as the terminal's session leader; pig is "$PIG" and its arguments follow in "$@".
	script string
	// status is whether the exit status of pig is readable: the script writes it to $STATUS.
	status bool
}

var hangupSetups = []hangupSetup{
	// pig is the session leader, as when a terminal window runs it directly: the kernel signals it at once.
	{name: "session leader"},
	// A shell that ignores SIGHUP stays alive under pig and records its status: the kernel signals only the shell, so the dead terminal is pig's only news.
	{name: "shell ignoring SIGHUP", script: `trap '' HUP; "$@"; echo $? > "$STATUS"`, status: true},
	// A shell that dies of SIGHUP while pig runs: the kernel hangs the terminal up first and signals pig only when the shell exits, as with a user's shell.
	{name: "shell dying of SIGHUP", script: `exec 3<&0; "$@" 0<&3 3<&- & echo $! > "$PIDFILE"; wait`},
}

// TestHangupWritesOneExitLogLine hangs up a real pig on a pseudo-terminal hangupRuns times in each setup and requires exactly
// one exit.log line, and exit 129 where the status is observable, each time (D102: one line per ending; Pi's exit status on a
// hung-up terminal). The test closes the master, so the kernel hangs the terminal up while the session's input ends and its next
// terminal write fails with EIO: all the endings race, as they do when a terminal window closes. The line names the first cause
// observed, `received SIGHUP` or `terminal gone: … (exit 129)`; which of them comes first depends on the setup.
//
// Measured on d7127ee2e5 (a container limited to 2 CPUs, six busy loops, pig as the session leader in a tmux pane): 100 of 100 hangups
// left three lines (`terminal gone: EOF (hung up)`, `received SIGHUP`, `terminal gone: write /dev/stdout: input/output error`)
// or two lines with no `received SIGHUP`.
func TestHangupWritesOneExitLogLine(t *testing.T) {
	bin := buildBinary(t)
	for _, setup := range hangupSetups {
		t.Run(setup.name, func(t *testing.T) {
			failures := 0
			causes := map[string]int{}
			for run := range hangupRuns {
				cause, problem := hangUpOnePig(t, bin, setup)
				causes[cause]++
				if problem != "" {
					failures++
					t.Errorf("run %d: %s", run, problem)
				}
			}
			t.Logf("first causes: %v", causes)
			if failures > 0 {
				t.Errorf("%d of %d hangups did not leave one line and exit 129", failures, hangupRuns)
			}
		})
	}
}

// syncBuffer collects the pseudo-terminal's output, which pig blocks on once its buffer fills.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) contains(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Contains(b.buf.Bytes(), []byte(s))
}

// hangUpOnePig starts pig on a pseudo-terminal, hangs the terminal up and returns the first cause its exit.log names, and what is wrong with the exit, or "".
func hangUpOnePig(t *testing.T, bin string, setup hangupSetup) (cause, problem string) {
	t.Helper()
	dir := t.TempDir()
	seedIntegrationSettings(t, dir)
	master, slave, err := openHangupPTY()
	if err != nil {
		t.Fatalf("open pseudo-terminal: %v", err)
	}
	defer func() { _ = master.Close() }()
	out := &syncBuffer{}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			_, _ = out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()

	statusPath, pidPath := filepath.Join(dir, "status"), filepath.Join(dir, "pig-pid")
	pigArgs := []string{bin, "--model", "test-faux/faux-1"}
	var cmd *exec.Cmd
	if setup.script == "" {
		cmd = exec.Command(pigArgs[0], pigArgs[1:]...)
	} else {
		cmd = exec.Command("sh", append([]string{"-c", setup.script, "sh"}, pigArgs...)...)
	}
	cmd.Dir = dir
	// The agent-directory variables the test process scopes would override PIG_HOME; empty them as the tmux panes do.
	cmd.Env = append(os.Environ(),
		"HOME="+dir, "PIG_HOME="+dir, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_QUIET_STARTUP=1",
		"PIG_CODING_AGENT_DIR=", "PIG_CODING_AGENT_SESSION_DIR=", "PI_CODING_AGENT_DIR=", "PI_CODING_AGENT_SESSION_DIR=",
		"STATUS="+statusPath, "PIDFILE="+pidPath)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			<-waited
		}
		<-readerDone
	}()

	agent := filepath.Join(dir, "agent")
	if !waitFor(startupReadyWait, func() bool {
		markers, _ := filepath.Glob(filepath.Join(agent, "crash-output", "*.log"))
		return len(markers) == 1 && out.contains("faux-1")
	}) {
		return "", "pig never showed its footer"
	}
	if err := master.Close(); err != nil {
		t.Fatalf("close the pseudo-terminal master: %v", err)
	}

	// The line is written before the process exits, so waiting for the exit also waits for the record. These waits are hang detectors, not delays the check relies on.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-ctx.Done():
		return "", "the session leader still running 30s after the hangup"
	}
	code := -1
	switch {
	case setup.status:
		raw, err := os.ReadFile(statusPath)
		if err != nil {
			return "", "no status file: " + err.Error()
		}
		code, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	case setup.script == "":
		if exit, ok := errors.AsType[*exec.ExitError](waitErr); ok {
			code = exit.ExitCode()
		}
	}
	if setup.script != "" && !setup.status {
		raw, err := os.ReadFile(pidPath)
		if err != nil {
			return "", "no pig pid: " + err.Error()
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if pid <= 0 || !waitFor(30*time.Second, func() bool { return processExited(pid) }) {
			return "", "pig still running 30s after the hangup"
		}
	}

	data, err := os.ReadFile(filepath.Join(agent, "exit.log"))
	if err != nil {
		return "", "no exit.log: " + err.Error()
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	switch {
	case len(lines) != 1:
		return "", "exit.log is " + strings.Join(lines, " | ")
	case strings.HasSuffix(lines[0], " received SIGHUP"):
		cause = "received SIGHUP"
	case strings.Contains(lines[0], " terminal gone: ") && strings.HasSuffix(lines[0], " (exit 129)"):
		cause = "terminal gone"
	default:
		return "", "exit.log names no hangup cause: " + lines[0]
	}
	if (setup.status || setup.script == "") && code != 129 {
		return cause, "exit status " + strconv.Itoa(code) + ", want 129 (exit.log: " + lines[0] + ")"
	}
	return cause, ""
}
