package coding

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// abortThenErrorProvider starts the first response and ends it as aborted once
// its request is cancelled; every later request fails with a retryable error.
type abortThenErrorProvider struct {
	calls   atomic.Int32
	started chan struct{}
}

func (p *abortThenErrorProvider) ID() string   { return "fake" }
func (p *abortThenErrorProvider) Close() error { return nil }
func (p *abortThenErrorProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	if p.calls.Add(1) == 1 {
		partial := sessionTestMessage("fake", "", ai.StopReasonPending, "")
		stream := newSessionTestStream(ai.StartEvent{Partial: partial})
		go func() {
			close(p.started)
			<-ctx.Done()
			aborted := sessionTestMessage("fake", "", ai.StopReasonAborted, "")
			_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: aborted})
		}()
		return stream, nil
	}
	failed := sessionTestMessage("fake", "", ai.StopReasonError, "429 Too Many Requests")
	return newSessionTestStream(ai.StartEvent{Partial: failed}, ai.ErrorEvent{Reason: ai.StopReasonError, Error: failed}), nil
}

// An aborted run's assistant message_end must reach the forwarder. Otherwise
// its assistant-end note stays queued and the next run's agent_end reports
// the aborted message's willRetry instead of its own.
func TestAbortedSessionRunKeepsAgentEndWillRetryAligned(t *testing.T) {
	svcs := newTestServices(t)
	for run := range 20 {
		provider := &abortThenErrorProvider{started: make(chan struct{})}
		sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
		if err != nil {
			t.Fatal(err)
		}
		ends := make(chan agent.AgentEndEvent, 8)
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for ev := range sess.Events() {
				if end, ok := ev.(agent.AgentEndEvent); ok {
					ends <- end
				}
			}
		}()

		abortCtx, abort := context.WithCancel(context.Background())
		go func() {
			<-provider.started
			abort()
		}()
		_, _ = sess.Send(abortCtx, "abort me")

		retryCtx, stopRetry := context.WithCancel(context.Background())
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			_, _ = sess.Send(retryCtx, "fail retryably")
		}()
		var retryEnd *agent.AgentEndEvent
		deadline := time.After(5 * time.Second)
		for retryEnd == nil {
			select {
			case end := <-ends:
				if last := lastAssistantMessage(end.Messages); last != nil && last.StopReason == ai.StopReasonError {
					retryEnd = &end
				}
			case <-deadline:
				t.Fatalf("run %d: no agent_end for the failed run", run)
			}
		}
		stopRetry()
		<-sent
		_ = sess.Close()
		<-drained
		if !retryEnd.WillRetry {
			t.Fatalf("run %d: agent_end.willRetry = false for a retryable error", run)
		}
	}
}
