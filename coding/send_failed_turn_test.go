package coding

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type sequencedProviderForSessionTests struct {
	turns [][]ai.AssistantMessageEvent
	calls *atomic.Int32
}

func (p sequencedProviderForSessionTests) ID() string { return "fake" }
func (p sequencedProviderForSessionTests) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	i := int(p.calls.Add(1)) - 1
	return newSessionTestStream(p.turns[i]...), nil
}
func (p sequencedProviderForSessionTests) Close() error { return nil }

// TestSendReportsFailedTurnOnMessageNotError pins the Pi contract that a
// failed model call does not make Send fail: the failure is data.
// Pi: packages/agent/src/agent.ts:373-382 (prompt() resolves),
// agent-loop.ts:245-253 (stopReason error/aborted ends the run without
// throwing), agent.ts:594-597 (turn_end with an assistant errorMessage sets
// state.errorMessage) and agent.ts:521 (cleared when the next run starts).
func TestSendReportsFailedTurnOnMessageNotError(t *testing.T) {
	failed := sessionTestMessage("fake", "", ai.StopReasonError, "429 Too Many Requests")
	provider := sequencedProviderForSessionTests{calls: new(atomic.Int32), turns: [][]ai.AssistantMessageEvent{
		{ai.StartEvent{Partial: failed}, ai.ErrorEvent{Reason: ai.StopReasonError, Error: failed}},
		{
			ai.StartEvent{Partial: sessionTestMessage("fake", "", ai.StopReasonPending, "")},
			ai.DoneEvent{Reason: ai.StopReasonStop, Message: sessionTestMessage("fake", "all good", ai.StopReasonStop, "")},
		},
	}}
	// Retries off: the scripted failure fails once instead of backing off.
	disableRetry := false
	svcs := newTestServicesWithSettings(t, icodingagent.Settings{
		Retry: &icodingagent.RetrySettingsJSON{Enabled: &disableRetry},
	})
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	msgs, err := sess.Send(t.Context(), "hi")
	if err != nil {
		t.Fatalf("Send returned an error for a failed model call: %v", err)
	}
	last := msgs[len(msgs)-1].Assistant
	if last == nil || last.StopReason != ai.StopReasonError || !strings.Contains(last.ErrorMessage, "429") {
		t.Fatalf("last message = %+v, want assistant with StopReason error and the 429 text", msgs[len(msgs)-1])
	}
	if got := sess.Agent().ErrorMessage(); got != last.ErrorMessage {
		t.Fatalf("Agent().ErrorMessage() = %q, want %q", got, last.ErrorMessage)
	}

	msgs, err = sess.Send(t.Context(), "again")
	if err != nil {
		t.Fatal(err)
	}
	if last = msgs[len(msgs)-1].Assistant; last == nil || last.StopReason != ai.StopReasonStop {
		t.Fatalf("second turn last message = %+v, want StopReason stop", msgs[len(msgs)-1])
	}
	if got := sess.Agent().ErrorMessage(); got != "" {
		t.Fatalf("Agent().ErrorMessage() = %q after a successful turn, want empty", got)
	}
}
