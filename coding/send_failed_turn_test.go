package coding

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
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

// abortingStreamProviderForSessionTests opens a stream and reports it. When
// respond is set it ends the stream with an aborted assistant message once the
// request context is cancelled; otherwise the stream never ends and the loop
// records the abort itself.
type abortingStreamProviderForSessionTests struct {
	started chan struct{}
	respond bool
}

func (p abortingStreamProviderForSessionTests) ID() string { return "fake" }
func (p abortingStreamProviderForSessionTests) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	partial := sessionTestMessage("fake", "", ai.StopReasonPending, "")
	if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
		return nil, err
	}
	close(p.started)
	if !p.respond {
		return stream, nil
	}
	go func() {
		<-ctx.Done()
		aborted := sessionTestMessage("fake", "", ai.StopReasonAborted, "Request was aborted")
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: aborted})
	}()
	return stream, nil
}
func (p abortingStreamProviderForSessionTests) Close() error { return nil }

// TestSendRetriedCallIsHealthyTurn: the last assistant message decides.
// Pi: agent-session.ts _handlePostAgentRun/_prepareRetry retry a retryable
// error and keep the failed attempt in raw session history (_omitRecoveryAttempt
// appends a context edit that omits it from agent.state.messages); agent.ts:521 clears state.errorMessage when the retry run
// starts, so only a final failure leaves it set (agent.ts:594-597).
func TestSendRetriedCallIsHealthyTurn(t *testing.T) {
	failed := sessionTestMessage("fake", "", ai.StopReasonError, "429 Too Many Requests")
	provider := sequencedProviderForSessionTests{calls: new(atomic.Int32), turns: [][]ai.AssistantMessageEvent{
		{ai.StartEvent{Partial: failed}, ai.ErrorEvent{Reason: ai.StopReasonError, Error: failed}},
		{
			ai.StartEvent{Partial: sessionTestMessage("fake", "", ai.StopReasonPending, "")},
			ai.DoneEvent{Reason: ai.StopReasonStop, Message: sessionTestMessage("fake", "recovered", ai.StopReasonStop, "")},
		},
	}}
	enabled := true
	svcs := newTestServicesWithSettings(t, icodingagent.Settings{
		Retry: &icodingagent.RetrySettingsJSON{Enabled: &enabled, MaxRetries: new(2), BaseDelayMs: new(1)},
	})
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	msgs, err := sess.Send(t.Context(), "hi")
	if err != nil {
		t.Fatalf("Send returned an error for a retried call: %v", err)
	}
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (failure, then retry)", got)
	}
	last := msgs[len(msgs)-1].Assistant
	if last == nil || last.StopReason != ai.StopReasonStop {
		t.Fatalf("last message = %+v, want assistant with StopReason stop", msgs[len(msgs)-1])
	}
	if got := sess.Agent().ErrorMessage(); got != "" {
		t.Fatalf("Agent().ErrorMessage() = %q after a successful retry, want empty", got)
	}
	// The failed attempt stays in the persisted session history but is omitted
	// from the projected transcript that Send returns.
	for _, m := range msgs {
		if m.Assistant != nil && m.Assistant.StopReason == ai.StopReasonError {
			t.Fatalf("returned transcript still holds the failed attempt: %+v", m.Assistant)
		}
	}
	persisted := 0
	for _, entry := range sess.Entries() {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			Type    string `json:"type"`
			Message struct {
				StopReason string `json:"stopReason"`
			} `json:"message"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if fields.Type == "message" && fields.Message.StopReason == string(ai.StopReasonError) {
			persisted++
		}
	}
	if persisted != 1 {
		t.Fatalf("session history holds %d failed attempts, want 1 (Pi keeps it in raw history)", persisted)
	}
}

// TestSendAbortedTurnIsNotAnError: an aborted turn is data too. Pi:
// agent-session.ts:2387 abort() aborts the run and awaits idle without
// rejecting prompt(); agent-loop.ts:245-253 ends the run on stopReason
// aborted; the message carries stopReason aborted, not error (agent.ts:541).
// PiG: Session.Abort ends Send with a nil error; a cancelled caller context
// is returned by Send as the context error (a Go-idiom difference: Pi has no
// caller context) unless the provider ends the stream with its own aborted
// message first, so the cancel case uses a stream that never ends. Both leave
// an aborted last assistant message.
func TestSendAbortedTurnIsNotAnError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respond bool
		stop    func(t *testing.T, sess *Session, cancel context.CancelFunc)
		wantErr error
	}{
		{"session-abort", true, func(t *testing.T, sess *Session, _ context.CancelFunc) {
			if err := sess.Abort(t.Context()); err != nil {
				t.Errorf("Abort: %v", err)
			}
		}, nil},
		{"context-cancel", false, func(_ *testing.T, _ *Session, cancel context.CancelFunc) { cancel() }, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disableRetry := false
			svcs := newTestServicesWithSettings(t, icodingagent.Settings{
				Retry: &icodingagent.RetrySettingsJSON{Enabled: &disableRetry},
			})
			provider := abortingStreamProviderForSessionTests{started: make(chan struct{}), respond: tc.respond}
			sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type result struct {
				msgs []agent.AgentMessage
				err  error
			}
			done := make(chan result, 1)
			go func() {
				msgs, err := sess.Send(ctx, "hi")
				done <- result{msgs, err}
			}()
			<-provider.started
			tc.stop(t, sess, cancel)
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Send did not return after the abort")
			}
			if !errors.Is(got.err, tc.wantErr) || (tc.wantErr == nil && got.err != nil) {
				t.Fatalf("Send error = %v, want %v", got.err, tc.wantErr)
			}
			last := lastAssistantMessage(got.msgs)
			if last == nil || last.StopReason != ai.StopReasonAborted {
				t.Fatalf("last assistant = %+v, want StopReason aborted (not error)", last)
			}
		})
	}
}
