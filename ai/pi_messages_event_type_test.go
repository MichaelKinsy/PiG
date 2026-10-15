package ai

import (
	"encoding/json"
	"testing"
)

// pi-messages.ts PiMessagesEvent: every wire `type` literal of the union decodes to its constant.
func TestPiMessagesEventTypeConstantsMatchTheWireLiterals(t *testing.T) {
	for wire, want := range map[string]PiMessagesEventType{
		"start": PiMessagesEventStart, "text_start": PiMessagesEventTextStart, "text_delta": PiMessagesEventTextDelta,
		"text_end": PiMessagesEventTextEnd, "thinking_start": PiMessagesEventThinkingStart,
		"thinking_delta": PiMessagesEventThinkingDelta, "thinking_end": PiMessagesEventThinkingEnd,
		"toolcall_start": PiMessagesEventToolcallStart, "toolcall_delta": PiMessagesEventToolcallDelta,
		"toolcall_end": PiMessagesEventToolcallEnd, "done": PiMessagesEventDone, "error": PiMessagesEventError,
	} {
		var event PiMessagesEvent
		if err := json.Unmarshal([]byte(`{"type":"`+wire+`"}`), &event); err != nil || event.Type != want {
			t.Errorf("%s: decoded %q, %v", wire, event.Type, err)
		}
	}
}

// pi-messages.ts createEventConverter returns `{ ...event, partial }` for a type outside its switch, and the stream loop pushes it and keeps
// reading; the agent loop's switch ignores it (agent-loop.ts streamAssistantResponse). Probed with Pi 1.1.0 (probe/sse/pm-unknown.txt): a
// `{"type":"weird"}` record between a text delta and done ends with stop and "Hel". PiG ended the turn with an error.
func TestPiMessagesUnknownEventTypeIsSkippedLikePi(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{
		map[string]any{"type": "start"},
		map[string]any{"type": "text_start", "contentIndex": 0},
		map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "Hel"},
		map[string]any{"type": "weird", "contentIndex": 0},
		map[string]any{},
		map[string]any{"type": "done", "reason": "stop", "usage": piMessagesTestUsage},
	}})

	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})

	if message.StopReason != StopReasonStop || message.ErrorMessage != "" || len(message.Content) != 1 {
		t.Fatalf("message = %+v", message)
	}
	if text, ok := message.Content[0].(TextContent); !ok || text.Text != "Hel" {
		t.Fatalf("content = %#v", message.Content)
	}
}
