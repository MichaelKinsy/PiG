//go:build unix

package codingagent

import (
	"os"
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
			// The killed job is a zombie until init reaps it.
			deadline := time.Now().Add(5 * time.Second)
			for processAlive(pid) {
				if time.Now().After(deadline) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Fatalf("the shell command's background job %d outlived the exit", pid)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
