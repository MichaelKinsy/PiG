package ai

import (
	"context"
	"testing"
	"time"
)

// lazy.ts:67-90 hands the request signal to the setup's provider call and forwards whatever the inner stream then emits: a cancellation reaches the inner producer through the context LazyStream gives setup, and the producer's aborted terminal event and result reach the outer stream.
func TestLazyStreamCancellationReachesTheInnerProducerAndItsAbortedTerminalIsForwarded(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{API: "test-api", ProviderID: "test-provider"}}
	started := make(chan struct{})
	outer := LazyStream(ctx, model, func(setupCtx context.Context) (*AssistantMessageEventStream, error) {
		inner := NewAssistantMessageEventStream()
		close(started)
		go func() {
			<-setupCtx.Done()
			aborted := &AssistantMessage{Content: []AssistantContentBlock{}, API: "test-api", Provider: "test-provider", Model: "m", StopReason: StopReasonAborted, ErrorMessage: "producer observed cancel"}
			_ = inner.Push(ErrorEvent{Reason: StopReasonAborted, Error: aborted})
			inner.End()
		}()
		return inner, nil
	})
	<-started
	cancel()
	waitCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	result, err := outer.ResultContext(waitCtx)
	if err != nil {
		t.Fatalf("outer result never settled after cancel: %v", err)
	}
	if result.StopReason != StopReasonAborted || result.ErrorMessage != "producer observed cancel" {
		t.Fatalf("outer result = %v %q, want the inner producer's aborted result", result.StopReason, result.ErrorMessage)
	}
	var types []AssistantEventType
	for event := range outer.Events(context.Background()) {
		types = append(types, event.EventType())
	}
	if len(types) != 1 || types[0] != "error" {
		t.Fatalf("forwarded events = %v, want [error]", types)
	}
}
