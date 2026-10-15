package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// packages/ai/src/utils/diagnostics.ts:6 DiagnosticErrorInfo.stack: a Pi-written transcript carries the JavaScript `stack` of a
// thrown error in `diagnostics[].error.stack` (extractDiagnosticError, diagnostics.ts:29). Go errors have no stack to extract, so the
// field is the wire form: it must survive decoding a Pi message and encoding it again, and extraction must not invent one.
// packages/ai/src/utils/diagnostics.ts:6,29 (DiagnosticErrorInfo.stack).
func TestDiagnosticErrorStackSurvivesAPiAssistantMessageRoundTrip(t *testing.T) {
	const stack = "Error: boom\n    at fetch (node:internal/deps/undici:1)\n    at async stream (pi-messages.ts:300)"
	piMessage, err := json.Marshal(map[string]any{
		"role": "assistant", "content": []any{}, "api": "pi-messages", "provider": "p", "model": "m", "stopReason": "error", "timestamp": 1,
		"usage":       map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
		"diagnostics": []any{map[string]any{"type": "provider_error", "timestamp": 2, "error": map[string]any{"name": "TypeError", "message": "boom", "stack": stack, "code": "ECONNRESET"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var message AssistantMessage
	if err := json.Unmarshal(piMessage, &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Diagnostics) != 1 || message.Diagnostics[0].Error == nil || message.Diagnostics[0].Error.Stack != stack {
		t.Fatalf("decoded diagnostics = %+v", message.Diagnostics)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var again struct {
		Diagnostics []struct {
			Error map[string]any `json:"error"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(encoded, &again); err != nil {
		t.Fatal(err)
	}
	if len(again.Diagnostics) != 1 || again.Diagnostics[0].Error["stack"] != stack || again.Diagnostics[0].Error["code"] != "ECONNRESET" {
		t.Fatalf("encoded error = %v, want the stack and code kept", again.Diagnostics)
	}
}

func TestDiagnosticErrorWithoutStackOmitsTheKey(t *testing.T) {
	encoded, err := json.Marshal(ExtractDiagnosticError(&codedTestError{message: "m", code: 7}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "stack") {
		t.Fatalf("encoded = %s, want no stack key (extractDiagnosticError leaves stack undefined, which JSON omits)", encoded)
	}
}
