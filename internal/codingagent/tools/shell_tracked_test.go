//go:build unix

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/mcp"
)

// A bash call's shell runs in its own process group, so a hangup or crash of PiG does not reach it. shell.ts
// trackDetachedChildPid records the shell and killTrackedDetachedChildren (called from the signal handlers, the dead
// terminal exit and the crash handler) kills each tree. A tracked call is killed without an abort, so it reports the
// shell's signal exit code, and it is untracked once it ends.
func TestKillTrackedDetachedChildrenKillsARunningBashCall(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	operations := CreateLocalBashOperations(&LocalBashOptions{BinDir: dir})
	done := make(chan BashOperationsResult, 1)
	go func() {
		result, err := operations.Exec(context.Background(), "echo $$ > "+pidFile+"; sleep 60", dir, BashOperationsExecOptions{})
		if err != nil {
			t.Errorf("Exec: %v", err)
		}
		done <- result
	}()
	waitFor(t, func() bool { _, err := os.Stat(pidFile); return err == nil })
	KillTrackedDetachedChildren()
	select {
	case result := <-done:
		if result.ExitCode == nil || *result.ExitCode != 128+int(syscall.SIGKILL) {
			t.Fatalf("exit code = %s, want %d", exitCodeText(result.ExitCode), 128+int(syscall.SIGKILL))
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the tracked shell was not killed")
	}
	trackedDetachedChildren.mu.Lock()
	defer trackedDetachedChildren.mu.Unlock()
	if len(trackedDetachedChildren.processes) != 0 {
		t.Fatalf("%d children still tracked after the call ended", len(trackedDetachedChildren.processes))
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Pi spawns and tracks a shell in one event-loop turn, so killTrackedDetachedChildren never misses a shell that
// exists. In Go the shell runs from its start, so a kill that arrives between the start and the tracking must wait for
// the tracking and then kill the shell.
func TestKillTrackedDetachedChildrenKillsAShellStartedButNotYetTracked(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	testHookAfterShellStart = func() {
		close(started)
		<-release
	}
	t.Cleanup(func() { testHookAfterShellStart = nil })
	dir := t.TempDir()
	operations := CreateLocalBashOperations(&LocalBashOptions{BinDir: dir})
	done := make(chan BashOperationsResult, 1)
	go func() {
		result, err := operations.Exec(context.Background(), "sleep 60", dir, BashOperationsExecOptions{})
		if err != nil {
			t.Errorf("Exec: %v", err)
		}
		done <- result
	}()
	<-started
	killed := make(chan struct{})
	go func() {
		KillTrackedDetachedChildren()
		close(killed)
	}()
	// Release the spawn only once the kill runs inside the window, so the test does not depend on the scheduler.
	waitForKillInSpawnWindow(killed)
	close(release)
	select {
	case result := <-done:
		if result.ExitCode == nil || *result.ExitCode != 128+int(syscall.SIGKILL) {
			t.Fatalf("exit code = %s, want %d", exitCodeText(result.ExitCode), 128+int(syscall.SIGKILL))
		}
	case <-time.After(testbudget.Wait(t)):
		// A call that ends the shell returns long before this. Whether the shell is still running, stopped (T) or
		// gone, and what else its group holds, is what macOS needs to show here: the wait ended on a CI runner once
		// and a shell that survives a kill is not observable from the exit code.
		t.Fatalf("the shell started before the kill survived it; children of the test and their process groups:\n%s", shellGroupState(os.Getpid()))
	}
	<-killed
}

// shellGroupState reports what ps shows for each child of the process parent that has a process group of its own (a
// shell a bash call started) and for every process in such a group: pid, ppid, pgid and stat, where a stat that starts
// with T is a stopped process. It tells a shell that a kill stopped and did not end from one that was never signalled
// and from a descendant that outlived the shell.
func shellGroupState(parent int) string {
	out, err := exec.Command("ps", "ax", "-o", "pid,ppid,pgid,stat,command").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("ps: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	self, own := strconv.Itoa(parent), strconv.Itoa(syscall.Getpgrp())
	child := func(f []string) bool { return len(f) >= 4 && f[1] == self && f[2] != own }
	groups := map[string]bool{}
	for _, line := range lines[1:] {
		if f := strings.Fields(line); child(f) {
			groups[f[2]] = true
		}
	}
	var report strings.Builder
	report.WriteString(lines[0] + "\n")
	for _, line := range lines[1:] {
		if f := strings.Fields(line); child(f) || (len(f) >= 4 && groups[f[2]]) {
			report.WriteString(line + "\n")
		}
	}
	return report.String()
}

// The report that a hung kill test prints must show a child of the test with its own process group and, once that
// group is stopped, the stop: a report that hid either would leave the next hit as unreadable as the last.
func TestShellGroupStateShowsAChildOfTheTestAndItsStop(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	stat := func() string {
		for line := range strings.SplitSeq(shellGroupState(os.Getpid()), "\n") {
			f := strings.Fields(line)
			if len(f) >= 6 && f[0] == strconv.Itoa(pid) && f[1] == strconv.Itoa(os.Getpid()) && f[2] == strconv.Itoa(pid) && f[4] == "sleep" && f[5] == "60" {
				return f[3]
			}
		}
		return ""
	}
	waitFor(t, func() bool { return stat() != "" })
	if got := stat(); strings.HasPrefix(got, "T") {
		t.Fatalf("the running child reports stat %q, want one that is not stopped", got)
	}
	if err := syscall.Kill(-pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.HasPrefix(stat(), "T") })
}

// waitForKillInSpawnWindow returns once KillTrackedDetachedChildren waits for the spawn lock (TryRLock fails while a
// writer waits) or, if the kill ignores that lock, once it has returned.
func waitForKillInSpawnWindow(killed <-chan struct{}) {
	for trackedDetachedChildren.spawning.TryRLock() {
		trackedDetachedChildren.spawning.RUnlock()
		select {
		case <-killed:
			return
		default:
			runtime.Gosched()
		}
	}
}

func exitCodeText(code *int) string {
	if code == nil {
		return "nil"
	}
	return strconv.Itoa(*code)
}

// The crash handler calls KillTrackedDetachedChildren after a panic. A panic between the shell's start and its tracking
// must not leave the spawn lock held, or the kill blocks forever instead of reporting the crash.
func TestKillTrackedDetachedChildrenReturnsAfterAPanicInTheSpawn(t *testing.T) {
	testHookAfterShellStart = func() { panic("spawn") }
	t.Cleanup(func() { testHookAfterShellStart = nil })
	dir := t.TempDir()
	func() {
		defer func() {
			if recovered := recover(); recovered != "spawn" {
				t.Fatalf("recovered %v, want the spawn panic", recovered)
			}
		}()
		_, _ = CreateLocalBashOperations(&LocalBashOptions{BinDir: dir}).Exec(context.Background(), "exit 0", dir, BashOperationsExecOptions{})
	}()
	killed := make(chan struct{})
	go func() {
		KillTrackedDetachedChildren()
		close(killed)
	}()
	select {
	case <-killed:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("KillTrackedDetachedChildren blocked on the spawn lock that the panic left held")
	}
}

// Pi's stdio transport kills the process group of every running MCP server from a process.once("exit") hook
// (packages/mcp/src/transports/stdio.ts installExitHook), so a dead-terminal exit, a crash or a termination signal
// leaves no server behind. Go runs no exit hooks, so the exits that skip the orderly session shutdown call
// KillTrackedDetachedChildren, which also ends the MCP servers' groups. A server that has not answered `initialize`
// is live too.
func TestKillTrackedDetachedChildrenEndsRunningMCPStdioServers(t *testing.T) {
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: "sleep", Args: []string{"60"}})
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	pid := transport.PID()
	if pid == 0 {
		t.Fatal("the server has no pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the server is not running: %v", err)
	}

	KillTrackedDetachedChildren()

	// The transport reaps its child, so a gone process reports ESRCH.
	waitFor(t, func() bool { return syscall.Kill(pid, 0) != nil })
}
