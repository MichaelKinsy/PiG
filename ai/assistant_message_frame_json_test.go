package ai

import (
	"encoding/json"
	"testing"
)

func assertFrameJSONEqual(t *testing.T, got AssistantMessageFrame, want []byte) {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(want) {
		t.Fatalf("decoded frame JSON\n got: %s\nwant: %s", encoded, want)
	}
}

func TestFrameJSONRoundTripsEveryVariant(t *testing.T) {
	frames := []AssistantMessageFrame{
		StartFrame{Partial: AssistantMessage{Content: []AssistantContentBlock{}, API: "a", Provider: "p", Model: "m", StopReason: StopReasonPending, Timestamp: 7, Diagnostics: []AssistantMessageDiagnostic{{Type: "d", Timestamp: 1}}}},
		TextStartFrame{Content: TextContent{Text: "t", TextSignature: "s"}},
		TextDeltaFrame{Delta: "d"},
		TextEndFrame{Content: "e"},
		ThinkingStartFrame{ContentIndex: 1, Content: ThinkingContent{Thinking: "th", Redacted: true}},
		ThinkingDeltaFrame{ContentIndex: 1, Delta: "x"},
		ThinkingEndFrame{ContentIndex: 1, Content: "y"},
		ToolCallStartFrame{ContentIndex: 2, ToolCall: ToolCall{ID: "c", Name: "n", Arguments: JsonObject{"a": "b"}}},
		ToolCallCheckpointFrame{ContentIndex: 2, JSON: `{"a":"b"`},
		ToolCallDeltaFrame{ContentIndex: 2, Delta: `}`},
		ToolCallEndFrame{ContentIndex: 2, ID: "c", Name: "n", Arguments: JsonObject{"a": "b"}},
	}
	for _, frame := range frames {
		encoded, err := json.Marshal(frame)
		if err != nil {
			t.Fatalf("marshal %s: %v", frame.FrameType(), err)
		}
		decoded, err := UnmarshalAssistantMessageFrame(encoded)
		if err != nil {
			t.Fatalf("unmarshal %s: %v", encoded, err)
		}
		if decoded.FrameType() != frame.FrameType() {
			t.Fatalf("decoded type %s != %s", decoded.FrameType(), frame.FrameType())
		}
		assertFrameJSONEqual(t, decoded, encoded)
	}
	if _, err := UnmarshalAssistantMessageFrame([]byte(`{"type":"done"}`)); err == nil {
		t.Fatal("unknown frame type decoded")
	}
}
