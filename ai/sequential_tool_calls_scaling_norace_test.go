//go:build !race

package ai

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// A message that streams many tool calls in a row costs each event what the event costs when one call streams: a call that is finished is not parsed, copied or published again by the events after it. The reader reads the tool calls at every event, as a renderer does (ObserveProjection reads what the publication holds, without copying it for an owner), so what grows with the calls is what settling and publishing do for the calls an event leaves alone. Parsing or copying a finished call again allocates, so the allocations of the stream grow with its events (linearly) only when they do not.
func TestScalingSequentialToolCallsPerEvent(t *testing.T) {
	for _, shape := range sequentialToolCallShapes {
		testscaling.Check(t, testscaling.Case{Name: shape.name + " sequential tool calls, read at every event", Size: 16, Bound: testscaling.Linear, Prepare: func(calls int) func() {
			body := shape.body(calls)
			options := StreamOptions{}
			baseURL := "http://scaling.invalid"
			if shape.name == "google" {
				// The Google adapter does not take a custom fetch.
				server := sequentialToolCallServer(body)
				t.Cleanup(server.Close)
				baseURL = server.URL
			} else {
				options.Fetch = scalingSSEClient(body)
			}
			provider := shape.provider(baseURL)
			return func() {
				stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), options)
				if err != nil {
					panic(err)
				}
				var blocks []AssistantContentProjection
				for _, event := range stream.ObservePublishedEvents(context.Background()) {
					if partial := eventPartial(event); partial != nil {
						blocks, _, _ = partial.ObserveProjection(blocks[:0])
					}
				}
				if result := stream.Result(); result.StopReason == StopReasonError {
					panic(result.ErrorMessage)
				}
			}
		}})
	}
}
