package daemon

// pi: packages/env/daemon/src/process.rs

// Ports the test of packages/env/daemon/src/sys/unix.rs "ending_a_group_kills_what_the_program_left_running" for every system, and the contracts
// of the group the three system files give it (sys/unix.rs and sys/windows.rs Group: new, wait_for_exit, end; kill_tree): the helper programs of main_test.go
// run the same on Linux, macOS and Windows, which the Unix-only shell test of process_unix_test.go cannot.

import (
	"io"
	"testing"
	"time"
)

const groupWait = 30 * time.Second

// The wrapper starts its sleeper only once the group exists (the helper waits for a line the test sends after startProgram returned), so the sleeper is inside it
// on Windows too, where a child started before the job assignment stays outside the job.
func TestEndingAGroupKillsWhatTheHelperProgramLeftRunning(t *testing.T) {
	started := startHelper(t, "wrapper")
	if _, err := io.WriteString(started.stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
	sleeper := watchProcess(t, readPid(t, started))
	if err := started.group.waitForExit(); err != nil {
		t.Fatal(err)
	}
	if sleeper.exited(0) {
		t.Fatal("the sleeper is gone before the group ended")
	}
	started.group.end()
	started.group.forget()
	if code, err := reapProgram(started.cmd); err != nil || code != 0 {
		t.Fatalf("the wrapper ended with %d, %v", code, err)
	}
	if !sleeper.exited(groupWait) {
		t.Fatal("the sleeper still runs after the group ended")
	}
}

func TestWaitForExitBlocksUntilTheProgramExitsAndLeavesItUnreaped(t *testing.T) {
	started := startHelper(t, "linger")
	waited := make(chan error, 1)
	go func() { waited <- started.group.waitForExit() }()
	select {
	case err := <-waited:
		t.Fatalf("waitForExit returned while the program ran: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := io.WriteString(started.stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("waitForExit: %v", err)
		}
	case <-time.After(groupWait):
		t.Fatal("waitForExit did not return after the program exited")
	}
	if started.cmd.ProcessState != nil {
		t.Fatal("the program was reaped by waitForExit")
	}
	started.group.end()
	started.group.forget()
	if code, err := reapProgram(started.cmd); err != nil || code != 0 {
		t.Fatalf("the program ended with %d, %v", code, err)
	}
}

func TestWaitForExitReturnsForAProgramThatExitedAndIsNotReaped(t *testing.T) {
	started := startHelper(t, "exit")
	// The helper closes its stdout as it exits; it is not reaped until reapProgram.
	if _, err := io.Copy(io.Discard, started.stdout); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- started.group.waitForExit() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("waitForExit: %v", err)
		}
	case <-time.After(groupWait):
		t.Fatal("waitForExit did not return for an exited program")
	}
	started.group.end()
	started.group.forget()
	if code, err := reapProgram(started.cmd); err != nil || code != 0 {
		t.Fatalf("the program ended with %d, %v", code, err)
	}
}

type reaped struct {
	code int
	err  error
}

// reapAsync reaps the program in the background: it ends when the program does, so the test sees a killed program's exit whether or not the system keeps its pid
// until it is reaped.
func reapAsync(started *program) <-chan reaped {
	done := make(chan reaped, 1)
	go func() {
		code, err := reapProgram(started.cmd)
		done <- reaped{code, err}
	}()
	return done
}

// kill reaches the program only while the group answers for it: forget, which the waiter calls before it reaps the program, ends the kills, so a kill that raced the
// reap never reaches a reused pid.
func TestGroupKillEndsTheProgramWhileItIsLive(t *testing.T) {
	started := startHelper(t, "linger")
	watchProcess(t, started.pid)
	done := reapAsync(started)
	started.group.kill()
	select {
	case got := <-done:
		if got.err != nil || got.code == 0 {
			t.Fatalf("the killed program ended with %d, %v", got.code, got.err)
		}
	case <-time.After(groupWait):
		t.Fatal("kill left the program running")
	}
	started.group.end()
	started.group.forget()
}

func TestGroupKillDoesNothingOnceTheGroupIsForgotten(t *testing.T) {
	started := startHelper(t, "linger")
	watchProcess(t, started.pid)
	done := reapAsync(started)
	started.group.forget()
	started.group.kill()
	select {
	case got := <-done:
		t.Fatalf("kill reached a forgotten group's program, which ended with %d, %v", got.code, got.err)
	case <-time.After(time.Second):
	}
	// The group no longer kills, so the test ends the program itself.
	killTree(started.pid)
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
	case <-time.After(groupWait):
		t.Fatal("killTree left the program running")
	}
}

// killTree is Node's killProcessTree: the program and everything it started, not waited for (Windows runs taskkill /F /T).
func TestKillTreeKillsTheProgramAndWhatItStarted(t *testing.T) {
	started := startHelper(t, "tree")
	child := watchProcess(t, readPid(t, started))
	watchProcess(t, started.pid)
	done := reapAsync(started)
	killTree(started.pid)
	select {
	case got := <-done:
		if got.err != nil || got.code == 0 {
			t.Fatalf("the killed program ended with %d, %v", got.code, got.err)
		}
	case <-time.After(groupWait):
		t.Fatal("killTree left the program running")
	}
	if !child.exited(groupWait) {
		t.Fatal("killTree left what the program started running")
	}
	started.group.end()
	started.group.forget()
}
