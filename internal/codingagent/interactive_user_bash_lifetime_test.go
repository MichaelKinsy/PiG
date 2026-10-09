package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func userBashOwnerProbe(t *testing.T, handler extension.HandlerFn) (*InteractiveMode, context.Context, context.CancelFunc) {
	t.Helper()
	m := newRunOnMainProbe(t)
	m.pendingMessagesContainer = tui.NewContainer()
	// The probe has no input loop. Production runs throttled renders on that loop, so route them to the dispatcher instead of the timer goroutine, which would read the component tree while the test goroutine mutates it.
	m.installRenderDispatcher()
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx, m.backgroundCtx = ctx, ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "owned", Handlers: map[string][]extension.HandlerFn{"user_bash": {handler}}}}, m.opts.CWD)
	return m, ctx, cancel
}

func userBashOwnedResult() *extension.UserBashEventResult {
	return &extension.UserBashEventResult{Result: map[string]any{"output": "owned result", "exitCode": 7, "cancelled": false, "truncated": false}}
}

// Cancellation must prevent even a successful late callback from starting local
// execution, rendering a block, or persisting into a replacement Session.
func TestInteractiveUserBashReplacementJoinsPendingHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		m, ctx, cancel := userBashOwnerProbe(t, func(args ...any) (any, error) {
			entered <- args[1].(context.Context)
			<-release
			return userBashOwnedResult(), nil
		})
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		defer func() { cancel(); <-loopDone; m.backgroundTasks.Wait() }()
		m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "must-not-run", false) })
		hookCtx := <-entered
		settled := make(chan error, 1)
		m.runOnMain(ctx, func() { settled <- m.settleActiveRun() })
		synctest.Wait()
		if hookCtx.Err() == nil {
			t.Error("replacement did not cancel pending hook")
		}
		returned := false
		select {
		case err := <-settled:
			returned = true
			t.Errorf("replacement passed a live hook: %v", err)
		default:
		}
		close(release)
		if !returned {
			if err := <-settled; err != nil {
				t.Fatal(err)
			}
		}
		m.backgroundTasks.Wait()
		if len(m.bashOrder) != 0 || len(m.userBashTasks) != 0 {
			t.Fatalf("late hook retained UI/tasks: %d/%d", len(m.bashOrder), len(m.userBashTasks))
		}
	})
}

func TestInteractiveUserBashShutdownInvalidatesQueuedResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) { return userBashOwnedResult(), nil })
		m.handleBashCommand(ctx, "must-not-run", false)
		synctest.Wait()
		if len(m.uiTaskCh) == 0 {
			t.Error("worker did not hand the result to its owner")
		}
		cancel()
		m.backgroundTasks.Wait()
		for len(m.uiTaskCh) > 0 {
			(<-m.uiTaskCh)()
		}
		if len(m.bashOrder) != 0 {
			t.Fatal("queued result mutated UI after shutdown")
		}
	})
}

func TestInteractiveUserBashShutdownInvalidatesQueuedCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) { return userBashOwnedResult(), nil })
		m.handleBashCommand(ctx, "must-not-run", false)
		(<-m.uiTaskCh)() // Admit the block, but leave its completion queued.
		synctest.Wait()
		before := strings.Join(m.chatContainer.Render(100), "\n")
		cancel()
		m.backgroundTasks.Wait()
		for len(m.uiTaskCh) > 0 {
			(<-m.uiTaskCh)()
		}
		if after := strings.Join(m.chatContainer.Render(100), "\n"); after != before {
			t.Fatalf("completion rendered after shutdown:\nbefore=%q\nafter=%q", before, after)
		}
	})
}

func TestInteractiveUserBashCancelledBeforeDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		called := false
		m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) { called = true; return userBashOwnedResult(), nil })
		defer cancel()
		m.abortFn()
		m.handleBashCommand(ctx, "must-not-run", false)
		synctest.Wait()
		m.backgroundTasks.Wait()
		if called || len(m.uiTaskCh) != 0 {
			t.Fatal("cancelled dispatch started a callback or queued UI")
		}
	})
}

func TestInteractiveUserBashShutdownCancelsAndDrainsHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		exited := make(chan struct{})
		m, ctx, cancel := userBashOwnerProbe(t, func(args ...any) (any, error) {
			close(entered)
			handlerCtx := args[1].(context.Context)
			<-handlerCtx.Done()
			close(exited)
			return nil, handlerCtx.Err()
		})
		m.handleBashCommand(ctx, "must-not-run", false)
		<-entered
		cancel()
		m.backgroundTasks.Wait()
		select {
		case <-exited:
		default:
			t.Fatal("shutdown returned without draining user_bash")
		}
	})
}

// Streaming may finish while a hook awaits. Upstream reads isStreaming after
// await emitUserBash (interactive-mode.ts:6752,6780), not on submission.
func TestInteractiveUserBashUsesStreamingStateAfterHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) {
			close(entered)
			<-release
			return userBashOwnedResult(), nil
		})
		m.turnActive.Store(true)
		m.isIdle = false
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		defer func() { cancel(); <-loopDone }()
		m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "must-not-run", false) })
		<-entered
		m.runOnMain(ctx, func() { m.turnActive.Store(false); m.isIdle = true; close(release) })
		m.backgroundTasks.Wait()
		if len(m.bashOrder) != 1 || len(m.pendingBashBlocks) != 0 || !m.isIdle {
			t.Fatalf("completed hook used stale state: blocks=%d pending=%d idle=%v", len(m.bashOrder), len(m.pendingBashBlocks), m.isIdle)
		}
	})
}

// blockingBashOperations runs until its context is cancelled, as a long `!` command does.
type blockingBashOperations struct{ started chan struct{} }

func (o blockingBashOperations) Exec(ctx context.Context, _, _ string, _ extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	close(o.started)
	<-ctx.Done()
	return extension.BashOperationsResult{}, errors.New("aborted")
}

// Pi's onEscape (interactive-mode.ts setupKeyHandlers) calls session.abortBash() when no agent run streams and a `!`
// command runs: the command stops, its block ends with "(cancelled)", and no run abort is requested.
func TestInteractiveEscapeCancelsRunningUserBash(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ops := blockingBashOperations{started: make(chan struct{})}
		m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) {
			return &extension.UserBashEventResult{Operations: ops}, nil
		})
		handle := &compactionUIHandle{}
		m.opts.SessionHandle = handle
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		defer func() { cancel(); <-loopDone; m.backgroundTasks.Wait() }()
		m.keybindings = DefaultKeybindingsManager()
		m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "sleep 30", false) })
		<-ops.started
		synctest.Wait()
		m.runOnMain(ctx, func() {
			if err := m.handleKey(ctx, "\x1b"); err != nil {
				t.Error(err)
			}
		})
		synctest.Wait()
		type state struct {
			blocks  int
			block   string
			running bool
			idle    bool
		}
		got := make(chan state, 1)
		m.runOnMain(ctx, func() {
			s := state{blocks: len(m.bashOrder), running: m.bashCancel != nil, idle: m.isIdle}
			if s.blocks == 1 {
				s.block = widthx.StripAnsi(strings.Join(m.bashOrder[0].Render(60), "\n"))
			}
			got <- s
		})
		s := <-got
		if s.blocks != 1 || !strings.Contains(s.block, "(cancelled)") || strings.Contains(s.block, "Running...") || s.running || !s.idle {
			t.Fatalf("Escape left the command running: blocks=%d running=%v idle=%v block=\n%s", s.blocks, s.running, s.idle, s.block)
		}
		if handle.aborts != 0 {
			t.Fatalf("Escape requested %d run aborts; upstream aborts only the bash command when no run streams", handle.aborts)
		}
	})
}
