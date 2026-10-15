package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// A context edit replaces content in place: { ...message, content } keeps every other member and the content member's position (session-manager.ts applyContextEditReplacement); an assistant's string content becomes one text block.
func TestContextEditReplacementKeepsMemberOrder(t *testing.T) {
	for _, tc := range []struct{ message, content, want string }{
		// node -e 'const m=JSON.parse(a), c=JSON.parse(b); console.log(JSON.stringify({...m, content: c}))'
		{`{"timestamp":1,"content":[{"text":"old","type":"text"}],"meta":{"z":1,"a":2},"role":"user"}`, `[{"type":"text","text":"new"}]`, `{"timestamp":1,"content":[{"type":"text","text":"new"}],"meta":{"z":1,"a":2},"role":"user"}`},
		{`{"role":"user","timestamp":1}`, `"new"`, `{"role":"user","timestamp":1,"content":"new"}`},
		{`{"timestamp":2,"content":[],"role":"assistant","api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop"}`, `"new"`, `{"timestamp":2,"content":[{"type":"text","text":"new"}],"role":"assistant","api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop"}`},
	} {
		var message agent.AgentMessage
		if err := json.Unmarshal([]byte(tc.message), &message); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(replaceContextContent(message, json.RawMessage(tc.content)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := jsonstringify.Canonicalize(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("replace content of %s with %s\n got %s\nwant %s", tc.message, tc.content, got, tc.want)
		}
	}
}
