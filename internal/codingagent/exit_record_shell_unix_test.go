//go:build unix

package codingagent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A shell command (a `!` command or the bash tool) runs in its own process group, so neither a terminal hangup nor
// pig's exit reaches it. Pi kills its tracked detached children on both exits that skip orderly shutdown
// (interactive-mode.ts emergencyTerminalExit and uncaughtCrash). Without that, a `!sleep 30` kept running after a
// closed terminal ended the session.
func TestExitWithoutShutdownKillsTheRunningShellCommand(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		code     int
	}{{"dead-terminal-running-shell", 129}, {"crash-running-shell", 1}} {
		t.Run(tc.scenario, func(t *testing.T) {
			agentDir := t.TempDir()
			output, code := runExitRecordChild(t, tc.scenario, agentDir)
			data, err := os.ReadFile(filepath.Join(agentDir, "shell-job-pid"))
			if err != nil {
				t.Fatalf("the shell command never started: %v\n%s", err, output)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			if code != tc.code {
				t.Fatalf("exit code = %d, want %d\n%s", code, tc.code, output)
			}
			// The killed job stays a zombie until its new parent reaps it, and a PID 1 that never reaps would leave
			// it one, so a zombie counts as stopped.
			deadline := time.Now().Add(5 * time.Second)
			for processRunning(pid) {
				if time.Now().After(deadline) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Fatalf("the shell command's background job %d outlived the exit", pid)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// processRunning reports whether pid is a process that has not exited: processAlive, minus a zombie. Linux shows the
// state in /proc; other systems through ps.
func processRunning(pid int) bool {
	if !processAlive(pid) {
		return false
	}
	if stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err == nil {
		// The state follows the parenthesized command name, which may itself hold ") ".
		if i := bytes.LastIndex(stat, []byte(") ")); i >= 0 && i+2 < len(stat) {
			return stat[i+2] != 'Z'
		}
		return true
	}
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		// ps fails for a pid that is gone.
		return processAlive(pid)
	}
	return !strings.HasPrefix(strings.TrimSpace(string(state)), "Z")
}
