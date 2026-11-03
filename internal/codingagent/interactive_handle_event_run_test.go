package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts handleEvent (3467-3865): agent_start, turn_start, queue_update,
// thinking_level_changed, agent_settled and bash_execution_update.

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi's agent_start handler only clears the pending tools and restores the escape handler. The terminal progress and the working status start at
// the run's first turn_start, so a run that emits both writes the progress sequence once.
func TestHandleEventAgentStartShowsNeitherProgressNorStatus(t *testing.T) {
	p := newEventProbe(t, true)
	p.workingVisible = true
	p.handleAgentEvent(agent.AgentStartEvent{})
	if len(*p.progress) != 0 || p.indicatorKind() != "" {
		t.Fatalf("agent_start: progress=%v status=%q, want neither", *p.progress, p.indicatorKind())
	}
	p.handleAgentEvent(agent.TurnStartEvent{})
	if !slices.Equal(*p.progress, []bool{true}) || p.indicatorKind() != "working" {
		t.Fatalf("turn_start: progress=%v status=%q, want [true] and working", *p.progress, p.indicatorKind())
	}
}

// The status a retry left shown stays until turn_start replaces it with the working one.
func TestHandleEventAgentStartKeepsTheRetryStatusUntilTurnStart(t *testing.T) {
	p := newEventProbe(t, false)
	p.workingVisible = true
	p.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000})
	p.handleAgentEvent(agent.AgentStartEvent{})
	if p.indicatorKind() != "retry" {
		t.Fatalf("agent_start replaced the retry status with %q", p.indicatorKind())
	}
	p.handleAgentEvent(agent.TurnStartEvent{})
	if p.indicatorKind() != "working" {
		t.Fatalf("turn_start left %q shown", p.indicatorKind())
	}
}

// auto_retry_start swaps in an Escape handler that cancels the retry delay; agent_start puts the main handler back, so Escape in the retried run
// aborts the run.
func TestHandleEventAgentStartRestoresTheMainEscapeHandler(t *testing.T) {
	for _, tc := range []struct {
		name         string
		agentStarted bool
		wantRetry    int
		wantAbort    bool
	}{
		{"delay", false, 1, false},
		{"after agent_start", true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := statusBorderMode(t, true)
			t.Cleanup(func() { m.clearStatusIndicator("") })
			m.keybindings = DefaultKeybindingsManager()
			ctx := t.Context()
			m.runCtx = ctx
			m.abortCtx, m.abortFn = context.WithCancel(ctx)
			h := &recordingCompactHandle{}
			m.opts.SessionHandle = h
			m.isIdle = false
			aborted := m.abortCtx
			m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000})
			if tc.agentStarted {
				m.handleAgentEvent(agent.AgentStartEvent{})
			}
			if err := m.dispatchKey(ctx, "\x1b"); err != nil {
				t.Fatal(err)
			}
			if h.abortRetryCount != tc.wantRetry || (aborted.Err() != nil) != tc.wantAbort {
				t.Fatalf("Escape: retry aborts=%d run aborted=%v, want %d and %v", h.abortRetryCount, aborted.Err() != nil, tc.wantRetry, tc.wantAbort)
			}
		})
	}
}

// auto_retry_end restores the main handler too, so Escape after a finished retry does not cancel a delay that is over.
func TestHandleEventAutoRetryEndRestoresTheMainEscapeHandler(t *testing.T) {
	m := statusBorderMode(t, true)
	t.Cleanup(func() { m.clearStatusIndicator("") })
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = t.Context()
	m.abortCtx, m.abortFn = context.WithCancel(m.runCtx)
	h := &recordingCompactHandle{}
	m.opts.SessionHandle = h
	m.isIdle = false
	m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000})
	m.handleAgentEvent(agent.AutoRetryEndEvent{Success: true, Attempt: 1})
	if err := m.dispatchKey(m.runCtx, "\x1b"); err != nil {
		t.Fatal(err)
	}
	if h.abortRetryCount != 0 {
		t.Fatalf("Escape cancelled a retry that had ended: %d", h.abortRetryCount)
	}
}

// queue_update refreshes the pending-messages display from the queue, whoever changed it.
func TestHandleEventQueueUpdateRefreshesThePendingMessages(t *testing.T) {
	p := newEventProbe(t, false)
	pending := func() string { return widthx.StripAnsi(strings.Join(p.pendingMessagesContainer.Render(100), "\n")) }
	p.agent.Steer(userMsg("QUEUED STEER"))
	p.agent.FollowUp(userMsg("QUEUED FOLLOW"))
	if strings.Contains(pending(), "QUEUED STEER") {
		t.Fatalf("precondition: pending %q", pending())
	}
	p.handleAgentEvent(agent.QueueUpdateEvent{Steering: []string{"QUEUED STEER"}, FollowUp: []string{"QUEUED FOLLOW"}})
	if got := pending(); !strings.Contains(got, "Steering: QUEUED STEER") || !strings.Contains(got, "Follow-up: QUEUED FOLLOW") {
		t.Fatalf("queue_update did not show the queue: %q", got)
	}
	p.agent.ClearAllQueues()
	p.handleAgentEvent(agent.QueueUpdateEvent{})
	if got := pending(); strings.Contains(got, "QUEUED") {
		t.Fatalf("queue_update kept a cleared queue: %q", got)
	}
}

// thinking_level_changed refreshes the editor border color and the footer from the effective level.
func TestHandleEventThinkingLevelChangedRefreshesTheEditorAndFooter(t *testing.T) {
	p := newEventProbe(t, false)
	p.agent.SetThinkingLevel(ai.ThinkingHigh)
	p.handleAgentEvent(agent.ThinkingLevelChangedEvent{Level: ai.ThinkingHigh})
	if p.editor.ThinkingLevel != "high" || p.thinkingLevel != "high" {
		t.Fatalf("editor level %q, mode level %q, want high", p.editor.ThinkingLevel, p.thinkingLevel)
	}
	p.agent.SetThinkingLevel(ai.ThinkingOff)
	p.handleAgentEvent(agent.ThinkingLevelChangedEvent{Level: ai.ThinkingOff})
	if p.editor.ThinkingLevel != "off" {
		t.Fatalf("editor level %q after the change to off", p.editor.ThinkingLevel)
	}
}

// agent_settled ends the session when an extension asked for shutdown during the run; ctx.shutdown() ends an idle session at once.
func TestHandleEventAgentSettledChecksTheShutdownRequest(t *testing.T) {
	newMode := func(t *testing.T) (*InteractiveMode, func()) {
		m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Model: &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}})
		m.tuiInst = tui.NewWithOutput(io.Discard, 80, 24)
		m.statusLine = NewFooterComponent(nil, "", nil)
		bridge := &captureUIBridge{}
		m.opts.SubprocessUIBridge = bridge
		m.wireSubprocessHostCallbacks()
		shutdown, ok := bridge.actions["shutdown"].(func())
		if !ok {
			t.Fatalf("shutdown host action missing or wrong type: %T", bridge.actions["shutdown"])
		}
		return m, shutdown
	}
	t.Run("during a run", func(t *testing.T) {
		m, shutdown := newMode(t)
		m.turnActive.Store(true)
		shutdown()
		if m.requestExit.Load() {
			t.Fatal("ctx.shutdown() during a run ended the session before the run settled")
		}
		m.turnActive.Store(false)
		m.handleAgentEvent(agent.AgentSettledEvent{})
		if !m.requestExit.Load() {
			t.Fatal("agent_settled did not end the session an extension asked to shut down")
		}
		if text := exitLogText(m.opts.AgentDir); !strings.Contains(text, "extension requested shutdown") {
			t.Fatalf("exit.log = %q", text)
		}
	})
	t.Run("idle", func(t *testing.T) {
		m, shutdown := newMode(t)
		shutdown()
		if !m.requestExit.Load() {
			t.Fatal("ctx.shutdown() did not end an idle session")
		}
	})
	t.Run("no request", func(t *testing.T) {
		m, _ := newMode(t)
		m.handleAgentEvent(agent.AgentSettledEvent{Aborted: true})
		if m.requestExit.Load() {
			t.Fatal("agent_settled ended a session nobody asked to shut down")
		}
	})
}

// bash_execution_update draws nothing: the callback of the bash command renders its output.
func TestHandleEventBashExecutionUpdateChangesNothing(t *testing.T) {
	p := newEventProbe(t, true)
	p.handleAgentEvent(agent.MessageStartEvent{Message: userMsg("USER")})
	chat, pending := p.chat(), len(p.pendingMessagesContainer.Render(100))
	id := "bash-1"
	p.handleAgentEvent(agent.BashExecutionUpdateEvent{ID: &id, Delta: "OUTPUT DELTA"})
	if p.chat() != chat || strings.Contains(p.chat(), "OUTPUT DELTA") || len(p.pendingMessagesContainer.Render(100)) != pending || len(*p.progress) != 0 || p.indicatorKind() != "" {
		t.Fatalf("bash_execution_update changed the screen: chat %q", p.chat())
	}
}

// A real run ends with agent_settled through the owner loop: ctx.shutdown() requested while the run streams ends the session only when the run's
// settled event is handled.
func TestHandleEventRunSettledEndsASessionAskedToShutDownDuringTheRun(t *testing.T) {
	m, ctx, _ := newLifecycleMode(t)
	m.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) {
		m.requestExtensionShutdown()
		return nil, nil
	})
	settled := m.turnSettled
	<-settled
	if m.requestExit.Load() {
		t.Fatal("ctx.shutdown() during the run ended the session before the run settled")
	}
	for !m.isIdle {
		(<-m.uiTaskCh)()
	}
	m.drainMainLoopOnce()
	if !m.requestExit.Load() {
		t.Fatal("the run's agent_settled did not end the session an extension asked to shut down")
	}
}
