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
