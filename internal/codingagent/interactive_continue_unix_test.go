//go:build unix

package codingagent

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// A SIGCONT that tui reports reaches rearm on the owner loop, once for a burst, and only while a renderer still owns the terminal when the owner loop gets to it.
func TestContinueHandlerRearmsOnTheOwnerLoopWhileTheTerminalIsOwned(t *testing.T) {
	for _, owned := range []bool{true, false} {
		mode := &InteractiveMode{uiTaskCh: make(chan func(), 2)}
		rearms := 0
		handle := mode.continueHandler(t.Context(), func() { rearms++ })
		handle(func() bool { return owned })
		handle(func() bool { return owned })
		var apply func()
		select {
		case apply = <-mode.uiTaskCh:
		case <-time.After(5 * time.Second):
			t.Fatal("continue handler did not post a re-arm task to uiTaskCh")
		}
		mode.backgroundTasks.Wait()
		if rearms != 0 {
			t.Fatal("the terminal was armed outside the owner loop")
		}
		apply()
		select {
		case <-mode.uiTaskCh:
			t.Fatal("a burst of SIGCONT posted two re-arms")
		default:
		}
		if want := map[bool]int{true: 1, false: 0}[owned]; rearms != want {
			t.Fatalf("owned=%t: %d re-arms, want %d", owned, rearms, want)
		}
	}
}

// A SIGCONT arriving while uiTaskCh is full and Stop() cancels backgroundCtx (while runCtx is still live, as on normal quit or Init-without-Run
// shutdown before Stop marks runEnded) must unblock backgroundTasks.Wait() promptly instead of hanging Stop().
func TestContinueHandlerStopDoesNotHangWhenUITaskChannelIsFull(t *testing.T) {
	runCtx := t.Context()
	bgCtx, bgCancel := context.WithCancel(runCtx)
	mode := &InteractiveMode{
		runCtx:           runCtx,
		backgroundCtx:    bgCtx,
		backgroundCancel: bgCancel,
		uiTaskCh:         make(chan func(), 1),
	}
	mode.runEnded.begin()
	// Fill uiTaskCh so postToMain blocks waiting for the owner loop.
	mode.uiTaskCh <- func() {}

	stopContinue := mode.installContinueHandler(runCtx)
	t.Cleanup(func() {
		stopContinue()
		bgCancel()
		mode.runEnded.end()
		mode.backgroundTasks.Wait()
	})
	// Invoke the installed handler directly (simulating a SIGCONT right before Stop).
	handle := mode.continueHandler(runCtx, func() {})
	handle(func() bool { return true })

	// Simulate Stop()'s teardown registered at interactive.go:1810-1818: backgroundCancel() then backgroundTasks.Wait() while runCtx is still uncanceled and
	// before runEnded.end() runs at Stop() line 33.
	done := make(chan struct{})
	go func() {
		bgCancel()
		mode.backgroundTasks.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// Unblock cleanup so the test can exit cleanly.
		mode.runEnded.end()
		<-done
		t.Fatal("Stop() hung waiting on backgroundTasks.Wait() after SIGCONT with uiTaskCh full: continueHandler posted on runCtx instead of backgroundCtx")
	}
}

// rearmOperations().stop() neither calls rawRestore (preserving the launch-time cooked termios and avoiding a cooked-mode transition while the reader runs)
// nor blocks waiting on inputReader.done (which would deadlock the owner loop if SIGTSTP caught the reader between poll and read and the shell consumed the input).
func TestRearmTerminalDoesNotRestoreCookedOrDeadlockOnBlockedReader(t *testing.T) {
	neverCloses := make(chan struct{})
	restored := false
	reader := &interactiveTerminalReader{
		cancel: func() {},
		done:   neverCloses,
	}
	mode := &InteractiveMode{
		tuiInst:     tui.NewWithOutput(io.Discard, 80, 24),
		inputReader: reader,
		rawRestore: func() {
			restored = true
		},
	}
	done := make(chan struct{})
	go func() {
		mode.rearmOperations().stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rearmOperations().stop() deadlocked waiting on a reader blocked in read(0)")
	}
	if restored {
		t.Fatal("rearmOperations().stop() called rawRestore (restoring cooked mode while reader runs and overwriting saved cooked state under raw-restoring shells)")
	}
}

// rearmWithOps runs stop, start, and full redraw in order when foreground is true, and parks the process group with kill(0) without touching
// the terminal when foreground is false (D227).
func TestRearmWithOpsRunsStopStartRenderOnlyWhenForeground(t *testing.T) {
	for _, foreground := range []bool{true, false} {
		mode := &InteractiveMode{}
		var order []string
		var parkedPids []int
		mode.rearmWithOps(suspendOperations{
			foreground:    func() (bool, error) { return foreground, nil },
			kill:          func(pid int) error { parkedPids = append(parkedPids, pid); return nil },
			stop:          func() { order = append(order, "stop") },
			start:         func() error { order = append(order, "start"); return nil },
			requestRender: func() { order = append(order, "render") },
		})
		if foreground {
			if len(order) != 3 || order[0] != "stop" || order[1] != "start" || order[2] != "render" {
				t.Fatalf("foreground=true: ops order = %v, want [stop start render]", order)
			}
			if len(parkedPids) != 0 {
				t.Fatalf("foreground=true: unexpectedly called kill(%v)", parkedPids)
			}
		} else {
			if len(order) != 0 {
				t.Fatalf("foreground=false: ops ran %v, want none (D227)", order)
			}
			if len(parkedPids) != 1 || parkedPids[0] != 0 {
				t.Fatalf("foreground=false: kill pids = %v, want [0] (D227 park)", parkedPids)
			}
		}
	}
	mode := &InteractiveMode{}
	if mode.rearmOperations().kill == nil {
		t.Fatal("rearmOperations().kill is nil; background SIGCONT would not park the process group")
	}
}
