package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A provider's onResponse hook runs inside the provider's response turn (openai-completions.ts awaits options.onResponse before start). A host callback it reaches, such as an extension handler, awaits through the observation of the turn that is running, not through the parked consumer whose context the request inherited.
func TestProviderResponseHookAwaitsOnTheProducerTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := observationProvider(t, APIOpenAICompletions, server.URL)
	defer func() { _ = provider.Close() }()
	hooked := 0
	opts := StreamOptions{MaxRetries: new(0), OnResponse: func(ctx context.Context, _ ProviderResponse, _ *Model) error {
		observation := StreamObservationFromContext(ctx)
		if observation == nil {
			t.Error("hook has no stream observation")
			return nil
		}
		hooked++
		return observation.AwaitExternal(func() error { return nil })
	}}
	ctx := WithStreamContinuations(t.Context())
	var text string
	err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		scoped := observation.Context(ctx)
		stream, err := provider.Stream(scoped, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), opts)
		if err != nil {
			return err
		}
		for event := range stream.Events(scoped) {
			if done, ok := event.(DoneEvent); ok {
				text = done.Message.Content[0].(TextContent).Text
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if hooked != 1 || text != "done" {
		t.Fatalf("hooked = %d, text = %q", hooked, text)
	}
}

// The hook inherits the context of a consumer that is parked in Await while the provider's response turn runs. StreamObservationFromContext must still return the running turn's observation, so an awaiting host callback releases the turn that holds execution (openai-completions.ts awaits options.onResponse inside the response turn; event-stream.ts runs one turn at a time).
func TestProviderResponseHookObservationWhileTheConsumerIsParked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := observationProvider(t, APIOpenAICompletions, server.URL)
	defer func() { _ = provider.Close() }()
	hookDone := make(chan struct{})
	var hookErr error
	opts := StreamOptions{MaxRetries: new(0), OnResponse: func(ctx context.Context, _ ProviderResponse, _ *Model) error {
		defer close(hookDone)
		observation := StreamObservationFromContext(ctx)
		if observation == nil {
			hookErr = errors.New("hook has no stream observation")
			return nil
		}
		executor := observation.turn.executor
		executor.mu.Lock()
		active := executor.active
		executor.mu.Unlock()
		if observation.turn != active {
			hookErr = errors.New("hook received the parked consumer's observation instead of the running producer turn's")
			return nil
		}
		return observation.AwaitExternal(func() error { return nil })
	}}
	ctx := WithStreamContinuations(t.Context())
	var text string
	err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		scoped := observation.Context(ctx)
		stream, err := provider.Stream(scoped, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), opts)
		if err != nil {
			return err
		}
		if err := observation.Await(func() error { <-hookDone; return nil }); err != nil {
			return err
		}
		for event := range stream.Events(scoped) {
			if done, ok := event.(DoneEvent); ok {
				text = done.Message.Content[0].(TextContent).Text
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if text != "done" {
		t.Fatalf("text = %q", text)
	}
}
