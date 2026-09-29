package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

// Pi consumes values already held by the SDK's generators after signal abort. Completions finalizes blocks before its signal check; Responses finishes its terminal response before the outer signal check.
// Probe: buffered-abort-pi.mjs / buffered-abort-pi.json in the rpc33-observation evidence, using the canonical matrix bodies and aborting the first delta.
func TestOpenAIBufferedAbortRetainsDeliveredBodyValues(t *testing.T) {
	var inputs struct {
		Bodies map[API]map[string]string `json:"bodies"`
	}
	data, err := os.ReadFile("../coding/testdata/rpc33-observation/inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &inputs); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		api     API
		shape   string
		types   []AssistantEventType
		content string
	}{
		{APIOpenAICompletions, "text", []AssistantEventType{EventStart, EventTextStart, EventTextDelta, EventTextDelta, EventTextEnd, EventError}, `[{"type":"text","text":"one two"}]`},
		{APIOpenAICompletions, "thinking", []AssistantEventType{EventStart, EventThinkingStart, EventThinkingDelta, EventThinkingDelta, EventTextStart, EventTextDelta, EventThinkingEnd, EventTextEnd, EventError}, `[{"type":"thinking","thinking":"one two","thinkingSignature":"reasoning_content"},{"type":"text","text":"answer"}]`},
		{APIOpenAICompletions, "tool", []AssistantEventType{EventStart, EventToolCallStart, EventToolCallDelta, EventToolCallEnd, EventError}, `[{"type":"toolCall","id":"call-r2","name":"read","arguments":{"path":"target.txt"}}]`},
		{APIOpenAIResponses, "text", []AssistantEventType{EventStart, EventTextStart, EventTextDelta, EventTextDelta, EventTextEnd, EventError}, `[{"type":"text","text":"one two","textSignature":"{\"v\":1,\"id\":\"msg_r2\"}"}]`},
		{APIOpenAIResponses, "thinking", []AssistantEventType{EventStart, EventThinkingStart, EventThinkingDelta, EventThinkingDelta, EventThinkingEnd, EventError}, `[{"type":"thinking","thinking":"one two","thinkingSignature":"{\"type\":\"reasoning\",\"id\":\"rs_r2\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"one two\"}]}"}]`},
		{APIOpenAIResponses, "tool", []AssistantEventType{EventStart, EventToolCallStart, EventToolCallDelta, EventToolCallEnd, EventError}, `[{"type":"toolCall","id":"call-r2|fc_r2","name":"read","arguments":{"path":"target.txt"}}]`},
	} {
		t.Run(string(test.api)+"/"+test.shape, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, inputs.Bodies[test.api][test.shape])
			}))
			defer server.Close()
			provider := observationProvider(t, test.api, server.URL)
			defer func() { _ = provider.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var stream *AssistantMessageEventStream
			var types []AssistantEventType
			aborted := false
			// The probe's for-await starts in the job that called stream(); the consumer holds its continuation across Stream and the first iterator read, as the Agent does (agent-loop.ts:402-411).
			err := RunStreamContinuation(WithStreamContinuations(ctx), func(observation *StreamObservation) error {
				var err error
				stream, err = provider.Stream(observation.Context(ctx), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{})
				if err != nil {
					return err
				}
				for event := range stream.Events(observation.Context(context.WithoutCancel(ctx))) {
					types = append(types, event.EventType())
					if !aborted {
						switch event.(type) {
						case TextDeltaEvent, ThinkingDeltaEvent, ToolCallDeltaEvent:
							aborted = true
							cancel()
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(types, test.types) {
				t.Errorf("buffered abort events=%v, want %v", types, test.types)
			}
			result := stream.Result()
			if result.StopReason != StopReasonAborted || result.ErrorMessage != "Request was aborted" {
				t.Errorf("result=%#v", result)
			}
			assertScratchJSON(t, result.Content, test.content)
		})
	}
}
