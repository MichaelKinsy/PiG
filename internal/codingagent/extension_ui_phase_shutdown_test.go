package codingagent

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

// newPhaseShutdownMode is an idle interactive mode with an owner loop whose input never arrives.
func newPhaseShutdownMode(t *testing.T) (*InteractiveMode, context.Context) {
	t.Helper()
	m, ctx := newCustomEditorDispatchMode(t)
	t.Cleanup(m.abortFn)
	m.runCtx = ctx
	m.isIdle = true
	m.tuiInst.Add(m.editor)
	m.inputReadCh = make(chan inputChunk)
	m.inputErrCh = make(chan error, 1)
	m.uiTaskCh = make(chan func(), 4)
	return m, ctx
}

// Pi's event-context shutdownHandler ends an idle session at once (interactive-mode.ts:2020-2024), so a session_start
// handler that calls ctx.shutdown() while startup awaits bindCurrentSessionExtensions ends the process. Pig awaits that
// bind with awaitExtensionUI; the bind still has an owner-loop step after the handler returns, which the loop that
// already shut down never runs. The phase must end instead of holding the exit forever, and its pending step must see
// the cancellation.
func TestShutdownDuringAwaitedExtensionPhaseEndsThePhase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newPhaseShutdownMode(t)
		step := make(chan error, 1)
		err := m.awaitExtensionUI(ctx, func(ctx context.Context) error {
			m.requestShutdown() // the session_start handler's ctx.shutdown()
			err := m.runOnMainAndWait(ctx, func() error { return nil })
			step <- err
			return err
		})
		if !errors.Is(err, errInteractiveShutdown) {
			t.Fatalf("awaitExtensionUI = %v, want errInteractiveShutdown", err)
		}
		if !m.tuiTornDown {
			t.Fatal("the loop did not finish shutdown")
		}
		if err := <-step; !errors.Is(err, errInteractiveShutdown) {
			t.Fatalf("abandoned owner-loop step = %v, want errInteractiveShutdown", err)
		}
	})
}

// Pi's shutdown() ends with process.exit(0) and never awaits the handler that asked for it
// (interactive-mode.ts:4251-4290), so a handler that ignores cancellation cannot hold the exit.
func TestShutdownDuringAwaitedExtensionPhaseDoesNotWaitForAHandlerThatIgnoresCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newPhaseShutdownMode(t)
		release := make(chan struct{})
		defer close(release)
		returned := make(chan error, 1)
		go func() {
			returned <- m.awaitExtensionUI(ctx, func(context.Context) error {
				m.requestShutdown()
				<-release // never observes its context
				return nil
			})
		}()
		synctest.Wait()
		select {
		case err := <-returned:
			if !errors.Is(err, errInteractiveShutdown) {
				t.Fatalf("awaitExtensionUI = %v, want errInteractiveShutdown", err)
			}
		default:
			t.Fatal("the shut-down session waited for a handler that ignores cancellation")
		}
		m.backgroundTasks.Wait() // Run's deferred join must not include the abandoned handler
	})
}

// /new during which a session_before_switch handler calls ctx.shutdown() exits 0 in Pi. The replacement's sentinel must
// not take the fatal runtime path that exits 1.
func TestNewSessionEndedByShutdownIsNotAFatalRuntimeError(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	m.opts.AgentDir = t.TempDir()
	sc := &SlashContext{
		Append:            func(string) {},
		NewSession:        func() error { return errInteractiveShutdown },
		FatalRuntimeError: func(prefix string, err error) error { return m.handleFatalRuntimeError(prefix, err) },
	}
	if err := newHandler(sc); !errors.Is(err, errInteractiveShutdown) {
		t.Fatalf("newHandler = %v, want errInteractiveShutdown", err)
	}
	if err := m.requestedExitError(); err != nil || m.requestExit.Load() {
		t.Fatalf("requestedExitError = %v requestExit = %v, want a clean exit", err, m.requestExit.Load())
	}
	if records := ReadCrashLog(CrashLogPath(m.opts.AgentDir)); len(records) != 0 {
		t.Fatalf("crash records = %+v", records)
	}
}

// A phase that completes before shutdown keeps its own result.
func TestAwaitedExtensionPhaseWithoutShutdownReturnsItsResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newPhaseShutdownMode(t)
		want := errors.New("handler failed")
		ran := false
		err := m.awaitExtensionUI(ctx, func(ctx context.Context) error {
			if err := m.runOnMainAndWait(ctx, func() error { ran = true; return nil }); err != nil {
				return err
			}
			return want
		})
		if !errors.Is(err, want) || !ran || m.tuiTornDown {
			t.Fatalf("err=%v ran=%v tornDown=%v", err, ran, m.tuiTornDown)
		}
	})
}
