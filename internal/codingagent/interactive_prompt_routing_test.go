package codingagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// newStreamingRoutingMode starts a turn that streams until the returned
// release function is called, with the owner loop draining posted tasks.
func newStreamingRoutingMode(t *testing.T) (*InteractiveMode, context.Context, func()) {
	t.Helper()
	m := newPendingDisplayHarness(t)
	provider := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	model := &ai.Model{ID: "m", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 800000}}
	m.opts.Model = model
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.slashRegistry = NewSlashRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	var once sync.Once
	release := func() { once.Do(func() { close(provider.release) }) }
	t.Cleanup(func() {
		release()
		deadline := time.Now().Add(5 * time.Second)
		for m.turnActive.Load() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		<-loopDone
	})
	m.handleSubmit(ctx, "first prompt")
	<-provider.started
	return m, ctx, release
}

// onLoop runs fn on the owner loop and waits for it.
func onLoop(m *InteractiveMode, ctx context.Context, fn func()) {
	done := make(chan struct{})
	m.runOnMain(ctx, func() {
		fn()
		close(done)
	})
	<-done
}

// Enter on slash-prefixed text that is not a registered command, while a run
// streams, is upstream prompt(text, { streamingBehavior: "steer" }): the text
// queues as a steering message. Interactive mode started a second turn that
// failed with "already processing", lost the text, and flipped the busy
// state to idle while the first run still streamed (MODES-02).
func TestInteractiveSlashTextWhileStreamingSteers(t *testing.T) {
	m, ctx, _ := newStreamingRoutingMode(t)
	m.promptTemplates = []PromptTemplate{{Name: "review", Content: "Review carefully: $ARGUMENTS"}}

	var steering []agent.AgentMessage
	var idle bool
	var chat string
	onLoop(m, ctx, func() {
		m.editor.SetText("/not-a-command also check X")
		_ = m.dispatchKey(ctx, "\r")
		m.editor.SetText("/review the tests")
		_ = m.dispatchKey(ctx, "\r")
	})
	time.Sleep(200 * time.Millisecond)
	onLoop(m, ctx, func() {
		steering, _ = m.agent.PendingMessages()
		idle = m.isIdle
		chat = widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	})
	if len(steering) != 2 {
		t.Fatalf("steering messages = %d, want 2", len(steering))
	}
	if got := extractAgentMessageText(steering[0]); got != "/not-a-command also check X" {
		t.Fatalf("first steer = %q", got)
	}
	if got := extractAgentMessageText(steering[1]); got != "Review carefully: the tests" {
		t.Fatalf("template steer = %q, want the expanded template", got)
	}
	if idle || !m.turnActive.Load() {
		t.Fatalf("busy state after the steer: isIdle=%v turnActive=%v, want busy", idle, m.turnActive.Load())
	}
	if strings.Contains(chat, "already processing") {
		t.Fatalf("a second turn was started:\n%s", chat)
	}
}

// Steering and follow-up input reaches extension input handlers first, with
// the streaming behavior, like upstream _queueUserInput → _runInputHandlers.
// A handler that handles the input keeps it out of the queue (MODES-13).
func TestInteractiveSteerAndFollowUpRunInputHandlers(t *testing.T) {
	m, ctx, _ := newStreamingRoutingMode(t)
	m.keybindings = otherColumnKeys() // Alt+Enter is follow-up in this column
	var mu sync.Mutex
	var seen []extension.InputEvent
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "input-probe", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			event := args[0].(extension.InputEvent)
			mu.Lock()
			seen = append(seen, event)
			mu.Unlock()
			if strings.HasPrefix(event.Text, "secret") {
				return extension.InputEventResultHandled{}, nil
			}
			return extension.InputEventResultTransform{Text: "redacted " + event.Text}, nil
		}},
	}}}, t.TempDir())

	var steering, followUps []agent.AgentMessage
	onLoop(m, ctx, func() {
		m.editor.SetText("secret steer")
		_ = m.dispatchKey(ctx, "\r")
		m.editor.SetText("visible steer")
		_ = m.dispatchKey(ctx, "\r")
		m.editor.SetText("visible follow-up")
		_ = m.dispatchKey(ctx, "\x1b[13;3u") // Alt+Enter
		steering, followUps = m.agent.PendingMessages()
	})

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("input handler calls = %d, want 3", len(seen))
	}
	for i, want := range []string{"steer", "steer", "followUp"} {
		if seen[i].StreamingBehavior != want || seen[i].Source != extension.InputSourceUser {
			t.Fatalf("input event %d = %+v, want behavior %q from the interactive source", i, seen[i], want)
		}
	}
	if len(steering) != 1 || extractAgentMessageText(steering[0]) != "redacted visible steer" {
		t.Fatalf("steering queue = %v, want only the transformed visible steer", steering)
	}
	if len(followUps) != 1 || extractAgentMessageText(followUps[0]) != "redacted visible follow-up" {
		t.Fatalf("follow-up queue = %v, want the transformed follow-up", followUps)
	}
}

// When input handling itself fails (a stale extension runner), upstream
// prompt() rejects: the error is shown and the input is not sent. Pig sent
// the raw text as if no handler had run. A handler that throws is only
// reported, as upstream's runner.emitInput catches it.
func TestInteractiveInputHandlerErrorStopsTheInput(t *testing.T) {
	m, ctx, _ := newStreamingRoutingMode(t)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "input-probe", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(...any) (any, error) { return nil, nil }},
	}}}, t.TempDir())
	m.newRunner.Invalidate("input rejected by a stale runner")

	var steering []agent.AgentMessage
	var chat string
	onLoop(m, ctx, func() {
		m.editor.SetText("do not send this")
		_ = m.dispatchKey(ctx, "\r")
		steering, _ = m.agent.PendingMessages()
		chat = widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	})
	if len(steering) != 0 {
		t.Fatalf("steering queue = %v, want nothing queued after the handler failed", steering)
	}
	if !strings.Contains(chat, "input rejected by a stale runner") {
		t.Fatalf("chat lacks the handler error:\n%s", chat)
	}
}

// A `!cmd` submitted while another runs is refused with upstream's warning
// and stays in the editor (MODES-16); starting it let the first command's
// completion mark the UI idle while the second still ran.
func TestBashCommandWhileAnotherRunsIsRefused(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.chatContainer = tui.NewContainer()
	m.bashCancel = func() {}
	m.runCtx = t.Context()
	m.abortCtx, m.abortFn = context.WithCancel(t.Context())
	m.handleSubmit(t.Context(), "!true")
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	if !strings.Contains(chat, "A bash command is already running. Press Esc to cancel it first.") {
		t.Fatalf("chat lacks the already-running warning:\n%s", chat)
	}
	if got := m.editor.Text(); got != "!true" {
		t.Fatalf("editor = %q, want the command kept", got)
	}
	if len(m.bashOrder) != 0 {
		t.Fatal("a second bash command was started")
	}
}

// A run that fails with a deadline error it did not get from the user's
// cancellation shows the error (MODES-17). Interactive mode treated every
// context.DeadlineExceeded like an Esc abort and showed nothing.
func TestTurnDeadlineErrorIsShown(t *testing.T) {
	m := newPendingDisplayHarness(t)
	model := &ai.Model{ID: "capture-1", Provider: captureStreamOptionsProvider{seen: make(chan capturedStreamRequest, 1)}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m.opts.Model = model
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.slashRegistry = NewSlashRegistry()
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model, PreparePrompt: func(context.Context, []agent.AgentMessage) ([]agent.AgentMessage, error) {
		return nil, fmt.Errorf("fetch context: %w", context.DeadlineExceeded)
	}})
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	defer func() {
		cancel()
		<-loopDone
	}()

	m.handleSubmit(ctx, "hello")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var chat string
		onLoop(m, ctx, func() { chat = widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n")) })
		if strings.Contains(chat, "Error: fetch context: context deadline exceeded") {
			return
		}
		if !m.turnActive.Load() && time.Now().After(deadline) {
			t.Fatalf("the deadline error was not shown:\n%s", chat)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A session replacement settles the active run first, like upstream's
// teardownCurrent awaiting session.abort(), so the aborted turn cannot
// persist into the session that replaces it.
func TestSettleActiveRunAbortsAndWaitsForTheRun(t *testing.T) {
	m, ctx, _ := newStreamingRoutingMode(t)
	onLoop(m, ctx, func() {
		if err := m.settleActiveRun(); err != nil {
			t.Error(err)
		}
	})
	if m.runStreaming() {
		t.Fatal("the run was still active after settleActiveRun")
	}
}

// An extension's abort cancels the run and waits until it settles, as
// upstream abort() awaits waitForIdle, and leaves the mode able to run the
// next prompt. Interactive mode cancelled the shared abort context without
// replacing it (so the next turn started already cancelled), and
// waitForIdle polled isIdle every 50 ms off the owner loop (GUARD-11).
func TestExtensionAbortSettlesTheRunAndTheNextPromptRuns(t *testing.T) {
	m, ctx, _ := newStreamingRoutingMode(t)
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	abort := bridge.actions["abort"].(func())
	waitForIdle := bridge.actions["waitForIdle"].(func(context.Context) error)

	abort()
	if err := waitForIdle(ctx); err != nil {
		t.Fatalf("waitForIdle after abort = %v, want nil", err)
	}
	if m.turnActive.Load() {
		t.Fatal("the run was still active after abort returned")
	}

	onLoop(m, ctx, func() { m.handleSubmit(ctx, "second prompt") })
	if err := waitForIdle(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var last *agent.AssistantMessage
		onLoop(m, ctx, func() {
			for _, message := range m.agent.Messages() {
				if message.Assistant != nil {
					last = message.Assistant
				}
			}
		})
		if last != nil && last.StopReason == ai.StopReasonStop {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the prompt after an extension abort did not complete; last assistant = %+v", last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A run error that mentions 401 is shown verbatim. Upstream shows the error;
// interactive mode replaced it with "Authentication expired" and started a
// github-copilot device login whatever the provider (MODES-14).
func TestTurnErrorMentioning401IsShownAndStartsNoLogin(t *testing.T) {
	m := newPendingDisplayHarness(t)
	model := &ai.Model{ID: "capture-1", Provider: captureStreamOptionsProvider{seen: make(chan capturedStreamRequest, 1)}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m.opts.Model = model
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.slashRegistry = NewSlashRegistry()
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model, PreparePrompt: func(context.Context, []agent.AgentMessage) ([]agent.AgentMessage, error) {
		return nil, errors.New("upstream 401 from anthropic")
	}})
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	defer func() {
		cancel()
		<-loopDone
	}()

	m.handleSubmit(ctx, "hello")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var chat string
		onLoop(m, ctx, func() { chat = widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n")) })
		if strings.Contains(chat, "Authentication expired") {
			t.Fatalf("a 401 in the error text started a login:\n%s", chat)
		}
		if strings.Contains(chat, "Error: upstream 401 from anthropic") {
			return
		}
		if !m.turnActive.Load() && time.Now().After(deadline) {
			t.Fatalf("the error was not shown:\n%s", chat)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
