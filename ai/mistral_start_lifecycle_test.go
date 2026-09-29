package ai

import (
	"context"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
)

func TestMistralStartsAfterHeadersBeforeFirstSSEChunk(t *testing.T) {
	// Pi stream emits start after requestMistralStream/onResponse settles and before reading the first SSE chunk.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		reader, writer := io.Pipe()
		defer func() { _ = reader.Close(); _ = writer.Close() }()
		stream, err := mistralUpstreamProvider(t).Stream(ctx, mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		events := make(chan AssistantMessageEvent, 2)
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer close(events)
			for event := range stream.Events(context.WithoutCancel(ctx)) {
				events <- event
			}
		}()
		synctest.Wait()
		select {
		case event := <-events:
			if _, ok := event.(StartEvent); !ok {
				t.Errorf("first event=%T, want start", event)
			}
		default:
			t.Error("successful response headers did not emit start while the SSE body was stalled")
		}
		cancel()
		<-done
		if result := stream.Result(); result.StopReason != StopReasonAborted {
			t.Fatal(result)
		}
		for event := range events {
			if _, ok := event.(ErrorEvent); !ok {
				t.Errorf("post-cancellation event=%T, want error", event)
			}
		}
	})
}
