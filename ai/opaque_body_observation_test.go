package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A caller-supplied fetch body does not reveal whether its bytes are buffered, so every read is treated as awaiting the network. The consumer then sees Pi's start-before-body state deterministically, however the fixture's bytes were produced (D82: readiness for custom Fetch).
func TestOpaqueFetchBodyObservationIsDeterministic(t *testing.T) {
	bodies := map[API]string{
		APIOpenAICompletions: "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"a\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
		APIOpenAIResponses:   "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[]}}\n\n",
	}
	for api, body := range bodies {
		t.Run(string(api), func(t *testing.T) {
			states := map[string]int{}
			for run := range 60 {
				fetch := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					response := &http.Response{StatusCode: 200, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": {"text/event-stream"}}}
					if run%2 == 0 {
						response.Body = io.NopCloser(strings.NewReader(body))
					} else {
						reader, writer := io.Pipe()
						go func() { _, _ = io.WriteString(writer, body); _ = writer.Close() }()
						response.Body = reader
					}
					return response, nil
				})}
				provider := observationProvider(t, api, "http://opaque.invalid/v1")
				var retained AssistantMessageEvent
				// A Pi consumer's for-await starts in the tick that created the stream; the Go consumer reserves its continuation before the provider runs, as the Agent does.
				ctx := WithStreamContinuations(t.Context())
				err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
					scoped := observation.Context(ctx)
					stream, err := provider.Stream(scoped, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{Fetch: fetch, MaxRetries: new(0)})
					if err != nil {
						return err
					}
					for event := range stream.Events(scoped) {
						if start, ok := event.(StartEvent); ok {
							retained = start
							data, err := json.Marshal(start.Partial.Content)
							if err != nil {
								return err
							}
							states[string(start.Partial.StopReason)+string(data)]++
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				// Pi's retained start reference advances with the producer; a frozen snapshot would not.
				if api == APIOpenAICompletions {
					if refreshed := RefreshEvent(retained).(StartEvent).Partial; refreshed.StopReason != StopReasonToolUse || len(refreshed.Content) != 1 {
						t.Fatalf("retained start did not advance: %#v", refreshed)
					}
				}
				_ = provider.Close()
			}
			if len(states) != 1 {
				t.Fatalf("start states = %v, want one deterministic state", states)
			}
			for state := range states {
				if state != "pending[]" {
					t.Fatalf("start state = %s, want pending[] (start precedes the unresolved body read)", state)
				}
			}
		})
	}
}
