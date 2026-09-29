package ai

import (
	"errors"
	"testing"
)

// awaitProviderFailure observes the asynchronous request outcome rather than requiring HTTP setup to finish before Stream returns.
func awaitProviderFailure(t *testing.T, stream *AssistantMessageEventStream, err error) error {
	t.Helper()
	if err != nil {
		return err
	}
	if stream == nil {
		t.Fatal("provider returned neither a stream nor an error")
	}
	result := stream.Result()
	if result.StopReason != StopReasonError && result.StopReason != StopReasonAborted {
		t.Fatalf("expected provider failure, got %#v", result)
	}
	return errors.New(result.ErrorMessage)
}

// drainObservedAssistantEvents is used only while a synchronous test producer is stopped at an explicit read handshake. It captures the same primitive values as upstream tests that observe stream.push before yielding the next input record.
func drainObservedAssistantEvents(t *testing.T, stream *AssistantMessageEventStream) []AssistantMessageEvent {
	t.Helper()
	stream.mu.Lock()
	count := len(stream.queue)
	stream.mu.Unlock()
	if count == 0 {
		return nil
	}
	events := make([]AssistantMessageEvent, 0, count)
	for event := range stream.Events(t.Context()) {
		events = append(events, mapAssistantEventPartial(event, (*AssistantMessage).Observe))
		if len(events) == count {
			break
		}
	}
	return events
}
