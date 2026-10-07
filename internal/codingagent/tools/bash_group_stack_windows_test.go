//go:build windows

package tools

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// The x/sys job-object wrappers take the buffer as a uintptr and are ordinary
// Go functions, so a goroutine stack that grows inside one moves a
// stack-allocated buffer and the kernel fills the old copy. The sweep below
// calls the job queries at every stack offset, in 8-byte steps, across 32 KiB,
// so the growth lands inside the wrapper at some offset. A zeroed accounting
// buffer makes processGroupMayHoldOutput report an empty job, and the bash
// tool then waits forever on a pipe a descendant still holds.
//
// The shell's descendant, the Windows sort, reads its standard input until end
// of file, so the job keeps its members until the test closes that pipe after
// the sweep, however long the sweep takes.
func TestWin_JobQueriesSurviveAGoroutineStackMove(t *testing.T) {
	cmd := exec.Command("cmd", "/c", `%SystemRoot%\System32\sort.exe >NUL`)
	setProcessGroup(cmd)
	stop, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	attachProcessGroup(cmd.Process)
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = killProcessGroup(cmd.Process)
			_ = cmd.Wait()
		}
		releaseProcessGroup(cmd.Process)
	})
	v, ok := bashJobs.Load(cmd.Process.Pid)
	if !ok {
		t.Fatal("the shell was not assigned to a job")
	}
	job := v.(windows.Handle)

	type result struct {
		mayHold bool
		members int
		err     error
	}
	query := func() result {
		members, err := openJobMembers(job)
		for _, h := range members {
			_ = windows.CloseHandle(h)
		}
		return result{mayHold: processGroupMayHoldOutput(cmd.Process), members: len(members), err: err}
	}
	for depth := range 1024 {
		for offset, pad := range stackPads {
			done := make(chan result, 1)
			go atStackDepth(depth, func() { pad(func() { done <- query() }) })
			got := <-done
			if got.err != nil || !got.mayHold || got.members == 0 {
				t.Fatalf("at depth %d, offset %d words: processGroupMayHoldOutput = %v, %d job members, err %v; the shell is still running", depth, offset, got.mayHold, got.members, got.err)
			}
		}
	}
	if err := stop.Close(); err != nil {
		t.Fatalf("close the shell's stdin: %v", err)
	}
	waited = true
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the shell failed after its stdin closed: %v", err)
	}
}

//go:noinline
func atStackDepth(depth int, f func()) {
	if depth == 0 {
		f()
		return
	}
	atStackDepth(depth-1, f)
}

// stackPads call f below a frame 0 to 7 words larger, so with atStackDepth
// the sweep reaches every 8-byte stack offset.
var stackPads = []func(func()){
	func(f func()) { f() },
	func(f func()) { var p [1]uintptr; padStack(p[:]); f() },
	func(f func()) { var p [2]uintptr; padStack(p[:]); f() },
	func(f func()) { var p [3]uintptr; padStack(p[:]); f() },
	func(f func()) { var p [4]uintptr; padStack(p[:]); f() },
	func(f func()) { var p [5]uintptr; padStack(p[:]); f() },
	func(f func()) { var p [6]uintptr; padStack(p[:]); f() },
	func(f func()) { var p [7]uintptr; padStack(p[:]); f() },
}

//go:noinline
func padStack(p []uintptr) {
	for i := range p {
		p[i] = uintptr(i)
	}
}
