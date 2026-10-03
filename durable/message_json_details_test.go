package durable

import (
	"encoding/json"
	"testing"
)

// Entries, context edits and documents store the pi-ai message as JSON. Pi's ToolResultMessage keeps details: null
// apart from an absent details (JSON.stringify writes null and omits undefined), so both survive a decode and encode.
func TestDecodeMessageKeepsToolResultDetailsAsPi(t *testing.T) {
	for _, wire := range []string{
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[{"type":"text","text":"x"}],"details":null,"isError":false,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"isError":true,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":{"step":2},"isError":false,"timestamp":1}`,
	} {
		message, err := DecodeMessage([]byte(wire))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != wire {
			t.Fatalf("round trip\n got %s\nwant %s", encoded, wire)
		}
	}
}

// Pi's ToolControl is `{ addTools?: readonly string[]; terminate?: true; handoff?: string }` and the tool task stores it
// after dropping undefined keys, so an absent key is omitted and an empty addTools list stays an empty list.
func TestToolControlJSONKeepsPresenceAsPi(t *testing.T) {
	handoff := "next"
	for _, tc := range []struct {
		control ToolControl
		want    string
	}{
		{ToolControl{}, `{}`},
		{ToolControl{AddTools: []string{}}, `{"addTools":[]}`},
		{ToolControl{AddTools: []string{"extra"}}, `{"addTools":["extra"]}`},
		{ToolControl{Terminate: true, Handoff: &handoff}, `{"terminate":true,"handoff":"next"}`},
	} {
		encoded, err := json.Marshal(tc.control)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != tc.want {
			t.Fatalf("control %s, want %s", encoded, tc.want)
		}
		var decoded ToolControl
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if again, _ := json.Marshal(decoded); string(again) != tc.want {
			t.Fatalf("round trip %s, want %s", again, tc.want)
		}
	}
}
