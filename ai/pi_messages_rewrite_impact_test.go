package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// packages/ai/src/api/pi-messages.ts:169-177 appendRewriteDiagnostic: a done or error event with `rewrite` appends one
// "pi_messages_rewrite" diagnostic whose details are `{ ...rewrite }` (every key the backend sent, none added), and
// PiMessagesRewriteImpact (pi-messages.ts:44-51) names the six keys of that object. There is no Pi test for it.
func TestPiMessagesRewriteDiagnosticDetailsDecodeAsPiMessagesRewriteImpact(t *testing.T) {
	for _, terminal := range []map[string]any{
		{"type": "done", "reason": "stop"},
		{"type": "error", "reason": "error", "errorMessage": "boom"},
	} {
		rewrite := map[string]any{"policyId": "policy-7", "policyVersion": 3, "changed": true, "tokenCountChange": -42, "messageCountChange": 2, "systemPromptChanged": true, "extra": "kept"}
		terminal["usage"] = piMessagesTestUsage
		terminal["rewrite"] = rewrite
		baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{terminal}})

		_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})

		if len(message.Diagnostics) != 1 || message.Diagnostics[0].Type != "pi_messages_rewrite" {
			t.Fatalf("%v: diagnostics = %+v", terminal["type"], message.Diagnostics)
		}
		details := message.Diagnostics[0].Details
		if details["extra"] != "kept" {
			t.Errorf("%v: details = %v, want every backend key kept", terminal["type"], details)
		}
		raw, err := json.Marshal(details)
		if err != nil {
			t.Fatal(err)
		}
		var impact PiMessagesRewriteImpact
		if err := json.Unmarshal(raw, &impact); err != nil {
			t.Fatal(err)
		}
		want := PiMessagesRewriteImpact{PolicyID: "policy-7", PolicyVersion: 3, Changed: true, TokenCountChange: -42, MessageCountChange: 2, SystemPromptChanged: true}
		if !reflect.DeepEqual(impact, want) {
			t.Errorf("%v: impact = %+v, want %+v", terminal["type"], impact, want)
		}
	}
}

func TestPiMessagesWithoutRewriteAddsNoDiagnostic(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{map[string]any{"type": "done", "reason": "stop", "usage": piMessagesTestUsage}}})
	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})
	if len(message.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want none", message.Diagnostics)
	}
}
