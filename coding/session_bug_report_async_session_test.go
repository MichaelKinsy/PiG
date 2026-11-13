package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// agent-session.ts:4489-4503 summarizeForBugReport: the caller awaits the summary and its signal is the request signal.
// Cancelling the caller while the summary request is in flight cancels that request and ends the call with an error.
func TestSummarizeForBugReportCancelsTheInFlightRequestWithTheCaller(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	started := make(chan struct{})
	requestCtx := make(chan context.Context, 1)
	h.session.Agent().SetStreamFunction(func(ctx context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		requestCtx <- ctx
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := h.session.SummarizeForBugReport(ctx, "it broke")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the summary request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("SummarizeForBugReport succeeded after the caller was cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SummarizeForBugReport did not return after the caller was cancelled: the request does not carry the caller's signal")
	}
	if err := (<-requestCtx).Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("request context error = %v, want context.Canceled", err)
	}
}
