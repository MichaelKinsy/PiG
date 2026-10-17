package ai

import (
	"context"
	"errors"
	"testing"
)

// packages/ai/src/types.ts:295-300 (fetchDeferred, cancelDeferred).
func TestLazyAPISetupOrderingCancellationAndLoadFailure(t *testing.T) {
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	api := LazyAPI(func(ctx context.Context) (*ProviderStreams, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, LazyAPICapabilities{})
	model := &Model{ID: "model", ProviderMeta: ProviderMetadata{API: "test-api", ProviderID: "test-provider"}}
	stream, err := api.StreamSimple(ctx, model, NormalizeContext(Context{}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if stream.isTerminated() {
		t.Fatal("setup completed before loader resolved")
	}
	cancel()
	result := stream.Result()
	if result.StopReason != StopReasonError || result.ErrorMessage != context.Canceled.Error() || result.Provider != "test-provider" || result.Model != "model" {
		t.Fatalf("setup rejection=%#v", result)
	}
	events := []AssistantEventType{}
	for event := range stream.Events(context.Background()) {
		events = append(events, event.EventType())
	}
	if len(events) != 1 || events[0] != "error" {
		t.Fatalf("events=%v", events)
	}
	failed := LazyAPI(func(context.Context) (*ProviderStreams, error) { return nil, errors.New("load failed") }, LazyAPICapabilities{FetchDeferred: true, CancelDeferred: true})
	fetch, err := failed.FetchDeferred(t.Context(), model, DeferredHandle{}, DeferredFetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if fetch.Result().ErrorMessage != "load failed" {
		t.Fatal("fetch setup error lost")
	}
	if err := failed.CancelDeferred(t.Context(), model, DeferredHandle{}, DeferredCancelOptions{}); err == nil || err.Error() != "load failed" {
		t.Fatal("cancel error lost", err)
	}
}
