package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/ai/src/utils/event-stream.ts AssistantMessageEventStream sets durationMs when the response ends; the
// agent loop adds thinkingLevel afterwards, so durationMs precedes it on the wire (agent-loop.ts:409).
func TestAgentMessageJSONCarriesDurationMsBeforeThinkingLevel(t *testing.T) {
	duration := int64(1250)
	message := AgentMessage{Assistant: &AssistantMessage{
		Role: RoleAssistant, API: ai.API("openai-responses"), Provider: "openai", ModelID: "gpt-5",
		Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}},
		Usage:      &ai.Usage{},
		StopReason: ai.StopReasonStop, Timestamp: 321, DurationMs: &duration, ThinkingLevel: ai.ThinkingHigh,
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	for decoder.More() {
		token, _ := decoder.Token()
		keys = append(keys, token.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(keys); n < 2 || keys[n-2] != "durationMs" || keys[n-1] != "thinkingLevel" {
		t.Fatalf("keys = %v, want ...durationMs, thinkingLevel", keys)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Assistant.DurationMs; got == nil || *got != duration {
		t.Fatalf("decoded durationMs = %v, want %d", got, duration)
	}
	without := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Usage: &ai.Usage{}, Timestamp: 1}}
	encoded, _ = json.Marshal(without)
	if json.Valid(encoded) && containsKey(t, encoded, "durationMs") {
		t.Fatalf("a message without a duration wrote durationMs: %s", encoded)
	}
}

func containsKey(t *testing.T, data []byte, key string) bool {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	_, ok := object[key]
	return ok
}

// A response the faux provider streams over time carries the elapsed time on the assistant message the agent keeps, and the
// tool result of the next turn does not borrow it.
func TestAgentRunRecordsTheResponseDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := e2eAgent(ai.FauxConfig{TokensPerSecond: 20, TokenSize: &ai.FauxTokenSize{Min: new(2), Max: new(2)}}, e2eText("one two three four five six seven eight nine ten"))
		messages := mustSend(t, a, "Count.")
		var assistant *AssistantMessage
		for _, message := range messages {
			if message.Assistant != nil {
				assistant = message.Assistant
			}
		}
		if assistant == nil || assistant.DurationMs == nil {
			t.Fatalf("assistant message has no durationMs: %+v", assistant)
		}
		if *assistant.DurationMs < 100 || *assistant.DurationMs > int64(time.Minute/time.Millisecond) {
			t.Fatalf("durationMs = %d, want the streamed response time", *assistant.DurationMs)
		}
		kept := a.MessagesSnapshot()
		last := kept[len(kept)-1].Assistant
		if last == nil || last.DurationMs == nil || *last.DurationMs != *assistant.DurationMs {
			t.Fatalf("kept message durationMs = %v, want %d", last, *assistant.DurationMs)
		}
	})
}

// A stream function that fails before returning a stream becomes an error response like Pi's lazyStream setup error: its
// timestamp is the request start (not the failure time) and it carries the durationMs its stream would have measured.
// upstream: packages/ai/src/api/lazy.ts createSetupErrorMessage; event-stream.ts #time. Seen as a field-presence difference in the
// rpc-abort-retry parity scenario against Pi 1.1.0.
func TestAgentStreamFunctionFailureIsTimedLikeALazySetupError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now().UnixMilli()
		a := mustNewAgent(AgentOptions{Model: &ai.Model{ID: "m", ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}}, StreamFn: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			time.Sleep(250 * time.Millisecond)
			return nil, errors.New("setup failed")
		}})
		var assistant *AssistantMessage
		a.Subscribe(func(_ context.Context, event AgentEvent) error {
			if end, ok := event.(MessageEndEvent); ok && end.Message.Assistant != nil {
				assistant = end.Message.Assistant
			}
			return nil
		})
		_, _ = a.Send(context.Background(), "hi")
		if assistant == nil || assistant.StopReason != ai.StopReasonError || assistant.ErrorMessage != "setup failed" {
			t.Fatalf("assistant = %+v", assistant)
		}
		if assistant.Timestamp != started {
			t.Fatalf("timestamp = %d, want the request start %d", assistant.Timestamp, started)
		}
		if assistant.DurationMs == nil || *assistant.DurationMs != 250 {
			t.Fatalf("durationMs = %v, want 250", assistant.DurationMs)
		}
	})
}
