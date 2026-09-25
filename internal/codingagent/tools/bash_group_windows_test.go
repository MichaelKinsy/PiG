//go:build windows

package tools

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestWin_KillProcessGroupReapsTree verifies killProcessGroup (taskkill /F /T)
// terminates a spawned process and its child tree on Windows, matching
// upstream killProcessTree. Runs natively on a Windows runner; shares the Win_
// prefix so CI can select it with -test.run=Win_.
func TestWin_KillProcessGroupReapsTree(t *testing.T) {
	// cmd spawns a long-lived child (ping loop) so there is a real tree to reap.
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	setProcessGroup(cmd) // CREATE_NEW_PROCESS_GROUP path; exercise it
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	time.Sleep(300 * time.Millisecond) // let the child spawn

	if err := killProcessGroup(cmd.Process); err != nil {
		t.Fatalf("killProcessGroup: %v", err)
	}

	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("process still running after killProcessGroup")
	}

	if pidAlive(t, pid) {
		t.Fatalf("pid %d still present in tasklist after kill", pid)
	}
}

// pidAlive reports whether a PID is still listed by tasklist.
func pidAlive(t *testing.T, pid int) bool {
	t.Helper()
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		return false // tasklist failed -> treat as gone rather than flake the test
	}
	return strings.Contains(string(out), fmt.Sprintf("%d", pid))
}
