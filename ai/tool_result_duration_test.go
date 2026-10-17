package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// packages/ai/src/types.ts:620-624 and packages/agent/src/agent-loop.ts:935-942: a tool result carries `durationMs` between
// `isError` and `timestamp` when the tool ran, and omits it otherwise.
func TestToolResultMessageDurationMsWire(t *testing.T) {
	duration := int64(30)
	message := ToolResultMessage{ToolCallID: "c", ToolName: "t", Content: []ToolResultMessageContent{}, DurationMs: &duration, Timestamp: 5}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"isError":false,"durationMs":30,"timestamp":5`) {
		t.Fatalf("wire %s, want durationMs between isError and timestamp", encoded)
	}
	var decoded ToolResultMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.DurationMs == nil || *decoded.DurationMs != 30 {
		t.Fatalf("decoded %+v %v", decoded, err)
	}
	absent, _ := json.Marshal(ToolResultMessage{ToolCallID: "c", ToolName: "t", Content: []ToolResultMessageContent{}, Timestamp: 5})
	if strings.Contains(string(absent), "durationMs") {
		t.Fatalf("wire %s has durationMs for a call that did not run", absent)
	}
	cloned := message.cloneMessage().(ToolResultMessage)
	*cloned.DurationMs = 99
	if *message.DurationMs != 30 {
		t.Fatal("a cloned message shares its durationMs")
	}
}

// packages/ai/src/types.ts:575-582: an assistant message carries `durationMs`, from its `timestamp` until the response
// ended, when the event stream saw the response start, and omits it for legacy messages.
func TestAssistantMessageDurationMsWire(t *testing.T) {
	duration := int64(250)
	message := AssistantMessage{Content: []AssistantContentBlock{}, API: "faux", Provider: "p", Model: "m", StopReason: StopReasonStop, Timestamp: 5, DurationMs: &duration}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"durationMs":250`) {
		t.Fatalf("wire %s, want durationMs", encoded)
	}
	var decoded AssistantMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.DurationMs == nil || *decoded.DurationMs != 250 {
		t.Fatalf("decoded %+v %v", decoded, err)
	}
	message.DurationMs = nil
	if absent, _ := json.Marshal(message); strings.Contains(string(absent), "durationMs") {
		t.Fatalf("wire %s has durationMs for a legacy message", absent)
	}
}
