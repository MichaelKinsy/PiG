package durable

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A stored message goes through DecodeMessage and back through encoding/json on the native path (a model request, a record
// written for a turn). On Go 1.27 encoding/json runs on the v2 engine: a number must round through float64 as JSON.parse does
// (1.0 -> 1, 1e21 -> 1e+21, 12345678901234567890 -> 12345678901234567000). The expected string is JSON.stringify's output for the
// same input in node. Negative zero (JSON.stringify writes 0) and invalid UTF-8 in a string (Go 1.27.1 escapes it as \ufffd where
// Pi's decoded text holds the character, which JSON.stringify writes raw) differ and are open: questions/dcore-history.md q131.
func TestDecodeMessageNumbersMatchJSONStringify(t *testing.T) {
	in := `{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"t","arguments":{"n":1.0,"big":1e21,"small":1e-7,"x":0.1,"i":12345678901234567890,"e":1E2}}],"api":"faux","provider":"faux","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1}`
	message, err := DecodeMessage([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	const want = `"arguments":{"n":1,"big":1e+21,"small":1e-7,"x":0.1,"i":12345678901234567000,"e":100}`
	if !strings.Contains(string(out), want) {
		t.Fatalf("arguments differ from JSON.stringify:\n got %s\nwant %s", out, want)
	}
}

// DecodeMessage reads a leading `"role":"<plain>"` without a first full decode and falls back to the probe decode for any other
// shape; both ways reach the same typed message.
func TestDecodeMessageRoleFastPathAndFallbackAgree(t *testing.T) {
	user := `"content":"hi","timestamp":1}`
	for name, tc := range map[string]struct {
		in   string
		role string
		fast bool
	}{
		"plain":           {`{"role":"user",` + user, "user", true},
		"escaped role":    {`{"role":"us\u0065r",` + user, "user", false},
		"role not first":  {`{"content":"hi","role":"user","timestamp":1}`, "user", false},
		"whitespace":      {`{ "role": "user", ` + user, "user", false},
		"unterminated":    {`{"role":"user`, "", false},
		"long role":       {`{"role":"` + strings.Repeat("a", 40) + `",` + user, "", false},
		"assistant plain": {`{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1}`, "assistant", true},
		"tool result":     {`{"role":"toolResult","toolCallId":"c","toolName":"t","content":[],"isError":false,"timestamp":1}`, "toolResult", true},
	} {
		role, fast := leadingRole([]byte(tc.in))
		if fast != tc.fast || (fast && role != tc.role) {
			t.Errorf("%s: leadingRole = %q, %v; want %q, %v", name, role, fast, tc.role, tc.fast)
		}
		if tc.role == "" {
			continue
		}
		message, err := DecodeMessage([]byte(tc.in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var got string
		switch message.(type) {
		case ai.UserMessage:
			got = "user"
		case ai.AssistantMessage:
			got = "assistant"
		case ai.ToolResultMessage:
			got = "toolResult"
		}
		if got != tc.role {
			t.Errorf("%s: decoded role %q, want %q", name, got, tc.role)
		}
	}
	if _, err := DecodeMessage([]byte(`{"role":"nobody","content":""}`)); err == nil || !strings.Contains(err.Error(), `unknown role "nobody"`) {
		t.Errorf("an unknown role: %v", err)
	}
}
