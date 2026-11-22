//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWin_KillProcessGroupReapsTree verifies killProcessGroup (taskkill /F /T)
// terminates a spawned process and its child tree on Windows, matching
// upstream killProcessTree. Runs natively on a Windows runner; shares the Win_
// prefix so CI can select it with -test.run=Win_.
func TestWin_KillProcessGroupReapsTree(t *testing.T) {
	// cmd spawns a long-lived child (ping loop) so there is a real tree to reap.
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
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

// TestWin_KillTrackedProcessGroupsReapsJobDescendants verifies the exit-path
// kill ends a tracked shell and its job descendants in one bounded pass.
func TestWin_KillTrackedProcessGroupsReapsJobDescendants(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	attachProcessGroup(cmd.Process)
	t.Cleanup(func() { releaseProcessGroup(cmd.Process) })
	v, ok := bashJobs.Load(cmd.Process.Pid)
	if !ok {
		t.Fatal("the shell was not assigned to a job")
	}
	job := v.(windows.Handle)

	var members []windows.Handle
	for {
		var err error
		members, err = openJobMembers(job)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) >= 2 {
			break
		}
		for _, h := range members {
			_ = windows.CloseHandle(h)
		}
		runtime.Gosched()
	}
	defer func() {
		for _, h := range members {
			_ = windows.CloseHandle(h)
		}
	}()

	TrackDetachedChild(cmd.Process)
	t.Cleanup(func() { UntrackDetachedChild(cmd.Process) })
	KillTrackedDetachedChildren()

	for _, h := range members {
		if event, err := windows.WaitForSingleObject(h, 0); err != nil || event != windows.WAIT_OBJECT_0 {
			t.Fatalf("a job member was still running after KillTrackedDetachedChildren (event %d, err %v)", event, err)
		}
	}
	_, _ = cmd.Process.Wait()
}

// TestWin_KillTrackedProcessGroupsBoundedExit proves that the exit-path kill
// (Pi 31b407dc63, #11889) bounds the whole shutdown cleanup (one multi-PID
// taskkill, TerminateJobObject, and the job-member wait) within exitKillTimeout
// (2,000 ms), rather than hanging indefinitely when a job member or taskkill
// fails to complete.
func TestWin_KillTrackedProcessGroupsBoundedExit(t *testing.T) {
	if exitKillTimeout != 2000*time.Millisecond {
		t.Fatalf("exitKillTimeout = %s, want 2s", exitKillTimeout)
	}
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	attachProcessGroup(cmd.Process)
	t.Cleanup(func() {
		_ = killProcessGroup(cmd.Process)
		_, _ = cmd.Process.Wait()
		releaseProcessGroup(cmd.Process)
	})
	v, ok := bashJobs.Load(cmd.Process.Pid)
	if !ok {
		t.Fatal("the shell was not assigned to a job")
	}
	job := v.(windows.Handle)
	for {
		members, err := openJobMembers(job)
		if err != nil {
			t.Fatal(err)
		}
		n := len(members)
		for _, h := range members {
			_ = windows.CloseHandle(h)
		}
		if n >= 2 {
			break
		}
		runtime.Gosched()
	}

	// A real Windows manual-reset event that is never signalled stands in for a
	// job member that refuses to exit within the shutdown budget: WaitForSingleObject
	// blocks for the remaining millisecond bound and returns WAIT_TIMEOUT.
	stuckEvent, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(stuckEvent) })

	var gotArgs []string
	var sawWaitMs []uint32
	start := time.Now()
	killTrackedProcessGroupsWithTimeout(
		[]*os.Process{cmd.Process},
		exitKillTimeout,
		func(c *exec.Cmd) error {
			gotArgs = slices.Clone(c.Args)
			return nil
		},
		func(_ windows.Handle, ms uint32) (uint32, error) {
			sawWaitMs = append(sawWaitMs, ms)
			return windows.WaitForSingleObject(stuckEvent, ms)
		},
	)
	elapsed := time.Since(start)

	if len(gotArgs) < 5 || !slices.Equal(gotArgs[1:], []string{"/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid)}) {
		t.Fatalf("taskkill args = %q, want [/F /T /PID %d]", gotArgs, cmd.Process.Pid)
	}
	if len(sawWaitMs) == 0 {
		t.Fatal("expected bounded WaitForSingleObject calls for the job members")
	}
	for _, ms := range sawWaitMs {
		if ms == windows.INFINITE || ms > uint32(exitKillTimeout/time.Millisecond) {
			t.Fatalf("WaitForSingleObject timeout = %d ms, want <= %d ms (not INFINITE)", ms, exitKillTimeout/time.Millisecond)
		}
	}
	if elapsed < 1500*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("bounded exit kill took %s, want around %s", elapsed, exitKillTimeout)
	}
	// TerminateJobObject still ran before the member wait, so the real shell tree is already dead.
	if event, err := windows.WaitForSingleObject(stuckEvent, 0); err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("stuckEvent unexpectedly signalled: event=%d err=%v", event, err)
	}
	_, _ = cmd.Process.Wait()
	if pidAlive(t, cmd.Process.Pid) {
		t.Fatalf("pid %d still alive after TerminateJobObject", cmd.Process.Pid)
	}
}
