//go:build unix

package codingagent

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestSuspendInputHandlerPropagatesError(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("suspend owner stopped")
	cancel(cause)
	mode := &InteractiveMode{runCtx: ctx, editor: tui.NewEditor(), isIdle: true, tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	t.Cleanup(mode.tuiInst.Stop)
	if err := mode.handleEditorAction(ctx, actionSuspend, "\x1a"); !errors.Is(err, cause) {
		t.Fatalf("input handler error=%v, want %v", err, cause)
	}
}

func TestSuspendContinuationUsesOwnerLoopAndSurfacesErrors(t *testing.T) {
	for _, failure := range []error{nil, errors.New("resume failed")} {
		ctx, cancel := context.WithCancel(t.Context())
		mode := &InteractiveMode{backgroundCtx: ctx, uiTaskCh: make(chan func(), 1)}
		called := false
		stop := mode.onSuspendContinue(func() error { called = true; return failure }, func() {})
		t.Cleanup(func() { cancel(); stop(); mode.backgroundTasks.Wait() })
		if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
		apply := <-mode.uiTaskCh
		if called {
			t.Fatal("continuation mutated the UI outside the owner loop")
		}
		apply()
		mode.backgroundTasks.Wait()
		stop()
		cancel()
		if !called || !errors.Is(mode.inputLoopErr, failure) {
			t.Fatalf("called=%t resume error=%v, want %v", called, mode.inputLoopErr, failure)
		}
	}
}

func TestSuspendShutdownJoinsContinuationWithoutRestart(t *testing.T) {
	for _, queued := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		mode := &InteractiveMode{backgroundCtx: ctx, uiTaskCh: make(chan func(), 1)}
		cancelled := make(chan struct{})
		stop := mode.onSuspendContinue(func() error { t.Error("restarted after shutdown"); return nil }, sync.OnceFunc(func() { close(cancelled) }))
		t.Cleanup(func() { cancel(); stop(); mode.backgroundTasks.Wait() })
		var apply func()
		if queued {
			if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
				t.Fatal(err)
			}
			apply = <-mode.uiTaskCh
		}
		cancel()
		mode.backgroundTasks.Wait()
		select {
		case <-cancelled:
		default:
			t.Fatal("continuation waiter exited without releasing temporary ownership")
		}
		if apply != nil {
			apply()
		}
		stop()
	}
}
