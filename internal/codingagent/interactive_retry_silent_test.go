package codingagent

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi's auto_retry_start swaps in an Escape handler that cancels the retry delay (interactive-mode.ts:3872-3878). A retry that a fused member
// keeps silent shows no status line and no final error, but Escape must still cancel its delay, until agent_start or auto_retry_end puts the
// main handler back.
func TestSilentRetryKeepsTheEscapeHandlerThatCancelsTheDelay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		then      agent.AgentEvent
		wantRetry int
		wantAbort bool
	}{
		{"delay", nil, 1, false},
		{"after agent_start", agent.AgentStartEvent{}, 0, true},
		{"after auto_retry_end", agent.AutoRetryEndEvent{Success: false, Attempt: 1, FinalError: "boom"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { SetDefaultRetryDecider(nil, false) })
			SetDefaultRetryDecider(func(int, int, ai.AssistantMessage) ai.RetryDecision { return ai.RetryDecision{} }, true)
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
			if m.activeStatusIndicator != nil {
				t.Fatalf("a silent retry showed the %q status", m.activeStatusIndicator.Kind)
			}
			if tc.then != nil {
				m.handleAgentEvent(tc.then)
			}
			if chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n")); strings.Contains(chat, "Retry failed") {
				t.Fatalf("a silent retry rendered its final error: %q", chat)
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
