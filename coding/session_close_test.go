package coding

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// abortableProvider starts a response and ends it as aborted only when its
// request is cancelled.
type abortableProvider struct{ started chan struct{} }

func (p *abortableProvider) ID() string   { return "fake" }
func (p *abortableProvider) Close() error { return nil }
func (p *abortableProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	partial := sessionTestMessage("fake", "", ai.StopReasonPending, "")
	stream := newSessionTestStream(ai.StartEvent{Partial: partial})
	go func() {
		close(p.started)
		<-ctx.Done()
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: sessionTestMessage("fake", "", ai.StopReasonAborted, "")})
	}()
	return stream, nil
}

// Upstream dispose() calls agent.abort(), so closing a Session stops its
// active run instead of leaving the provider call and tools running.
func TestCloseAbortsInFlightSend(t *testing.T) {
	svcs := newTestServices(t)
	provider := &abortableProvider{started: make(chan struct{})}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, sendErr := sess.Send(context.Background(), "long task")
		done <- sendErr
	}()
	<-provider.started
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Send still running after Close")
	}
}

// A Send on a closed Session ends at once instead of running a turn.
func TestSendAfterCloseDoesNotRun(t *testing.T) {
	svcs := newTestServices(t)
	provider := &abortableProvider{started: make(chan struct{})}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	done := make(chan error, 1)
	go func() {
		_, sendErr := sess.Send(context.Background(), "after close")
		done <- sendErr
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send on a closed Session returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send on a closed Session hung")
	}
}

// Abort cancels the active run and returns once it has ended (upstream
// AgentSession.abort awaits waitForIdle).
func TestAbortWaitsForRunToEnd(t *testing.T) {
	svcs := newTestServices(t)
	provider := &abortableProvider{started: make(chan struct{})}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	go func() {
		for range sess.Events() { //nolint:revive // drain
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sess.Send(context.Background(), "long task")
	}()
	<-provider.started
	if err := sess.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sess.agent.IsStreaming() || !sess.mu.TryLock() {
		t.Fatal("Abort returned before the run ended")
	}
	sess.mu.Unlock()
	<-done
	if last := lastAssistantMessage(sess.Messages()); last == nil || last.StopReason != ai.StopReasonAborted {
		t.Fatalf("last assistant %+v, want an aborted response", last)
	}
}
