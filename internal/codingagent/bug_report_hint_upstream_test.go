package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestBugReportHintsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-bug-report-hint.test.ts:19
	t.Run("identifies extensions with frames in a crash stack", func(t *testing.T) {
		// D2: command examples use the PiG binary name.
		want := "A stack frame came from loaded extension `npm:pi-observational-memory`, which may be involved. Try disabling it with `pig config`, or run `pig -ne` to confirm."
		if got := FormatCrashExtensionHint([]string{"npm:pi-observational-memory"}); got != want {
			t.Fatalf("hint = %q, want %q", got, want)
		}
		if got := FormatCrashExtensionHint(nil); got != "" {
			t.Fatalf("absent hint = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-bug-report-hint.test.ts:26
	t.Run("does not suggest reports for retryable provider failures", func(t *testing.T) {
		m := bugHintEventMode(t)
		for _, failure := range []string{"500 Internal Server Error", "502 Bad Gateway", "503 Service Unavailable", "504 Gateway Timeout", "429 Too Many Requests", "Provider overloaded", "Network connection lost", "Request timed out"} {
			endBugHintMessage(m, ai.StopReasonError, failure)
		}
		if got := bugHintCount(m); got != 0 {
			t.Fatalf("retryable hints = %d", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-bug-report-hint.test.ts:44
	t.Run("does not suggest reports for cancellations", func(t *testing.T) {
		m := bugHintEventMode(t)
		endBugHintMessage(m, ai.StopReasonError, "This operation was aborted")
		endBugHintMessage(m, ai.StopReasonError, "Request cancelled")
		endBugHintMessage(m, ai.StopReasonAborted, "")
		if got := bugHintCount(m); got != 0 {
			t.Fatalf("cancellation hints = %d", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-bug-report-hint.test.ts:54
	t.Run("suggests reports for unexpected errors", func(t *testing.T) {
		m := bugHintEventMode(t)
		endBugHintMessage(m, ai.StopReasonError, "Unexpected internal state")
		if got := bugHintCount(m); got != 1 {
			t.Fatalf("unexpected error hints = %d, want 1", got)
		}
	})
}

func TestBugReportHintOnlyOnce(t *testing.T) {
	m := bugHintEventMode(t)
	endBugHintMessage(m, ai.StopReasonError, "Unexpected internal state")
	endBugHintMessage(m, ai.StopReasonError, "Another unexpected error")
	if got := bugHintCount(m); got != 1 {
		t.Fatalf("one-time hints = %d, want 1", got)
	}
}

func bugHintEventMode(t *testing.T) *InteractiveMode {
	t.Helper()
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg(""))
	prepareAssistantEventTest(t, m)
	return m
}

func endBugHintMessage(m *InteractiveMode, stop ai.StopReason, detail string) {
	message := assistantMsg("")
	message.Assistant.StopReason, message.Assistant.ErrorMessage = stop, detail
	m.handleAgentEvent(agent.MessageStartEvent{Message: message})
	m.handleAgentEvent(agent.MessageEndEvent{Message: message})
}

func bugHintCount(m *InteractiveMode) int {
	return strings.Count(strings.Join(m.chatContainer.Render(120), "\n"), "/bug")
}
