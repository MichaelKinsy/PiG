//go:build integration && !windows

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hangupRuns is how many hung-up sessions TestHangupWritesOneExitLogLine checks. The defect it pins was a race between the
// kernel's SIGHUP, the input reader's EOF and a failed terminal write, each of which recorded its own exit.log line: it hit
// every run on a loaded host, but each outcome (three lines, or two without the signal's) came up in about half of them, so
// the check repeats rather than trusting one lucky run.
const hangupRuns = 100

// TestHangupWritesOneExitLogLine hangs up a real pig in a tmux pane hangupRuns times and requires exactly one exit.log line,
// naming SIGHUP, each time (D102: one line per ending). tmux closes the pane's pseudo-terminal master, so the kernel sends
// SIGHUP while the session's input ends and its next terminal write fails with EIO: all three endings race, as they do when
// a terminal window closes.
//
// Measured on d7127ee2e5 (tern-ci:v7, --cpus 2, six busy loops): 30 hangups wrote three lines (`terminal gone: EOF (hung up)`,
// `received SIGHUP`, `terminal gone: write /dev/stdout: input/output error`) in 17 runs, and two lines with no
// `received SIGHUP` in 13.
//
// Pi leaves no trace, so only the exit code and the terminal restore are Pi's; they are pinned elsewhere
// (TestHungUpTerminalEOFExits129, D186).
func TestHangupWritesOneExitLogLine(t *testing.T) {
	requireTmux(t)
	bin := buildBinary(t)
	failures := 0
	for run := range hangupRuns {
		if problem := hangUpOnePig(t, bin); problem != "" {
			failures++
			t.Errorf("run %d: %s", run, problem)
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d hangups did not leave exactly one `received SIGHUP` line", failures, hangupRuns)
	}
}

// hangUpOnePig starts pig as the pane's own process, hangs the pane up and returns what is wrong with the exit.log it left, or "".
func hangUpOnePig(t *testing.T, bin string) string {
	t.Helper()
	dir := t.TempDir()
	seedIntegrationSettings(t, dir)
	session := "pig-hup-" + randID()
	// `exec` makes pig the pane's process (pane_pid), so the pane's hangup reaches pig with no shell between.
	command := strings.Join([]string{
		"cd", dir, "&&", "exec",
		"env", "PIG_HOME=" + dir, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_QUIET_STARTUP=1",
		bin, "--model", "test-faux/faux-1",
	}, " ")
	if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "34", command).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = tmuxCommand("kill-session", "-t", session).Run() })
	pid := waitForPanePID(t, session)
	waitForPaneText(t, session, "faux-1", startupReadyWait)

	agent := filepath.Join(dir, "agent")
	if markers, _ := filepath.Glob(filepath.Join(agent, "crash-output", "*.log")); len(markers) != 1 {
		return "no live session marker before the hangup"
	}
	// kill-session closes the pty master: the kernel hangs the terminal up and signals the session.
	if out, err := tmuxCommand("kill-session", "-t", session).CombinedOutput(); err != nil {
		t.Fatalf("tmux kill-session: %v\n%s", err, out)
	}
	// The line is written before the process exits, so waiting for the exit also waits for the record. This is a hang
	// detector, not a delay the check relies on.
	if !waitFor(30*time.Second, func() bool { return processExited(pid) }) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		return "pig still running 30s after the hangup"
	}
	data, err := os.ReadFile(filepath.Join(agent, "exit.log"))
	if err != nil {
		return "no exit.log: " + err.Error()
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], " received SIGHUP") {
		return "exit.log is " + strings.Join(lines, " | ")
	}
	return ""
}
