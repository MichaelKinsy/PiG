package agent

import (
	"encoding/json"
	"testing"
)

// JSON.stringify writes a message's members in the order the object was built, and a value an extension supplied (`details`) in the order it was written. The message a Node handler receives (the `context` event) or the wire shows is that text, not a map's sorted keys. upstream: coding-agent/src/core/messages.ts:29-67 (message shapes), 100-137 (createCustomMessage and the summary creators), agent-session.ts:3781-3792 (recordBashResult).
func TestAgentMessageJSONKeepsWrittenMemberOrder(t *testing.T) {
	for _, tc := range []struct{ name, wire string }{
		{"toolResult details", `{"role":"toolResult","toolCallId":"c","toolName":"t","content":[{"type":"text","text":"x"}],"details":{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]},"isError":false,"timestamp":1}`},
		{"custom", `{"role":"custom","customType":"probe","content":"note","display":true,"details":{"zeta":1,"alpha":{"yy":2,"bb":3}},"timestamp":1}`},
		{"custom without details", `{"role":"custom","customType":"probe","content":"note","display":false,"timestamp":1}`},
		{"bashExecution", `{"role":"bashExecution","command":"ls","output":"x","exitCode":0,"cancelled":false,"truncated":false,"fullOutputPath":"/tmp/o","timestamp":1,"excludeFromContext":true}`},
		{"branchSummary", `{"role":"branchSummary","summary":"s","fromId":"abc","timestamp":1}`},
		{"compactionSummary", `{"role":"compactionSummary","summary":"s","tokensBefore":10,"timestamp":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var message AgentMessage
			if err := json.Unmarshal([]byte(tc.wire), &message); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.wire {
				t.Errorf("decoded and encoded\n%s\nwant\n%s", encoded, tc.wire)
			}
			cloned, err := json.Marshal(message.Clone())
			if err != nil || string(cloned) != tc.wire {
				t.Errorf("clone encoded\n%s, %v\nwant\n%s", cloned, err, tc.wire)
			}
		})
	}
}

// Go code builds custom messages as maps. Their members are written in Pi's order (messages.ts:123-137), and a member a role does not define follows them sorted.
func TestAgentMessageCustomMapIsWrittenInPiMemberOrder(t *testing.T) {
	message := AgentMessage{Custom: map[string]any{
		"timestamp": int64(1), "details": json.RawMessage(`{"zeta":1,"alpha":2}`), "display": true, "content": "note", "customType": "probe", "role": "custom", "extra": "x", "another": "y",
	}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"custom","customType":"probe","content":"note","display":true,"details":{"zeta":1,"alpha":2},"timestamp":1,"another":"y","extra":"x"}`
	if string(encoded) != want {
		t.Fatalf("encoded\n%s\nwant\n%s", encoded, want)
	}
	unknown := AgentMessage{Custom: map[string]any{"role": "other", "b": 1, "a": 2}}
	if encoded, err := json.Marshal(unknown); err != nil || string(encoded) != `{"a":2,"b":1,"role":"other"}` {
		t.Fatalf("unknown role encoded %s, %v", encoded, err)
	}
}

// Content blocks of a custom message are written as Pi's tools and extensions write them: {type, text, textSignature} and {type, data, mimeType} (packages/ai/src/types.ts).
func TestAgentMessageCustomContentBlocksAreWrittenInPiMemberOrder(t *testing.T) {
	message := AgentMessage{Custom: map[string]any{"role": "custom", "customType": "probe", "display": true, "timestamp": int64(1), "content": []any{
		map[string]any{"text": "a", "type": "text"}, map[string]any{"mimeType": "image/png", "data": "aW1n", "type": "image"},
	}}}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"role":"custom","customType":"probe","content":[{"type":"text","text":"a"},{"type":"image","data":"aW1n","mimeType":"image/png"}],"display":true,"timestamp":1}`
	if string(encoded) != want {
		t.Fatalf("encoded\n%s\nwant\n%s", encoded, want)
	}
}
