//go:build windows

package tools

import (
	"os/exec"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWin_KillProcessGroupReapsJobDescendants sets the shell up as production
// does (suspended start, then job assignment and resume) and checks that
// killProcessGroup ends the descendant it started, not only the shell.
func TestWin_KillProcessGroupReapsJobDescendants(t *testing.T) {
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

	// The job holds the shell and, once it starts, ping. Wait for two members.
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

	if err := killProcessGroup(cmd.Process); err != nil {
		t.Fatalf("killProcessGroup: %v", err)
	}
	for _, h := range members {
		// killProcessGroup returns only after every member exited, so each handle is already signalled.
		if event, err := windows.WaitForSingleObject(h, 0); err != nil || event != windows.WAIT_OBJECT_0 {
			t.Fatalf("a job member was still running after killProcessGroup (event %d, err %v)", event, err)
		}
	}
	_, _ = cmd.Process.Wait()
}
