package codingagent

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi interactive-mode.ts:3235,6735 awaits the hook inside an async submit callback;
// editor.ts invokes that callback without blocking terminal input. The dispatch
// context retains the current cancellation signal (runner.ts:860).
func TestInteractiveUserBashYieldsInputLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		m.pendingMessagesContainer = tui.NewContainer()
		ctx, cancel := context.WithCancel(t.Context())
		m.runCtx = ctx
		m.backgroundCtx = ctx
		m.abortCtx, m.abortFn = context.WithCancel(ctx)
		entered, release := make(chan struct{}), make(chan struct{})
		m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "slow", Handlers: map[string][]extension.HandlerFn{
			"user_bash": {func(...any) (any, error) {
				close(entered)
				<-release
				return nil, errors.New("do not run locally")
			}},
		}}}, m.opts.CWD)
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		defer func() { close(release); cancel(); <-loopDone; m.backgroundTasks.Wait() }()
		submitted := make(chan struct{})
		m.runOnMain(ctx, func() {
			m.editor.SetText("!must-not-run")
			if err := m.handleKey(ctx, "\r"); err != nil {
				t.Error(err)
			}
			close(submitted)
		})
		<-entered
		synctest.Wait()
		select {
		case <-submitted:
		default:
			t.Fatal("Enter blocks the input loop while user_bash awaits its callback")
		}
		typed := make(chan string, 1)
		m.runOnMain(ctx, func() {
			if err := m.handleKey(ctx, "x"); err != nil {
				t.Error(err)
			}
			typed <- m.editor.Text()
		})
		if got := <-typed; got != "x" {
			t.Fatalf("pending hook swallowed input: %q", got)
		}
	})
}

func TestInteractiveUserBashKeepsCurrentCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		ctx, cancel := context.WithCancel(t.Context())
		m.runCtx, m.backgroundCtx = ctx, ctx
		m.abortCtx, m.abortFn = context.WithCancel(ctx)
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "signal", Handlers: map[string][]extension.HandlerFn{
			"user_bash": {func(args ...any) (any, error) {
				entered <- args[1].(context.Context)
				<-release
				return nil, errors.New("do not run locally")
			}},
		}}}, m.opts.CWD)
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		defer func() { close(release); cancel(); <-loopDone; m.backgroundTasks.Wait() }()
		m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "must-not-run", false) })
		handlerCtx := <-entered
		m.abortFn()
		synctest.Wait()
		if !errors.Is(handlerCtx.Err(), context.Canceled) {
			t.Fatalf("user_bash lost current cancellation: %v", handlerCtx.Err())
		}
	})
}
