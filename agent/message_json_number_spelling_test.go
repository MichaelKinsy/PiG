package agent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// JSON.parse reads every number spelling as one JavaScript number, so Pi keeps a session message whose integer member is written 1.767225603e12 or 1767225603000.0 (hand-edited
// or written by another tool). encoding/json refuses those spellings for an int64 field, which dropped the whole message from the context and get_messages.
func TestAgentMessageDecodeKeepsIntegerMembersWrittenAsExponentOrFraction(t *testing.T) {
	const usage = `"usage":{"input":1e2,"output":5.0,"cacheRead":0,"cacheWrite":0,"totalTokens":1.05e2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	for name, tc := range map[string]struct {
		raw   string
		check func(AgentMessage) bool
	}{
		"user exponent":       {`{"role":"user","content":"hi","timestamp":1.767225603e12}`, func(m AgentMessage) bool { return m.User != nil && m.User.Timestamp == 1767225603000 }},
		"user whole fraction": {`{"role":"user","content":"hi","timestamp":1767225603000.0}`, func(m AgentMessage) bool { return m.User != nil && m.User.Timestamp == 1767225603000 }},
		"assistant": {`{"role":"assistant","content":[],"api":"a","provider":"p","model":"m",` + usage + `,"stopReason":"stop","timestamp":1.5e12,"durationMs":2.5e1}`, func(m AgentMessage) bool {
			return m.Assistant != nil && m.Assistant.Timestamp == 1500000000000 && m.Assistant.DurationMs != nil && *m.Assistant.DurationMs == 25 &&
				m.Assistant.Usage.Input == 100 && m.Assistant.Usage.Output == 5 && m.Assistant.Usage.TotalTokens == 105
		}},
		"tool result": {`{"role":"toolResult","toolCallId":"c","toolName":"t","content":[],"isError":false,"timestamp":1e12,"durationMs":2.5e1}`, func(m AgentMessage) bool {
			return m.ToolResult != nil && m.ToolResult.Timestamp == 1000000000000 && m.ToolResult.DurationMs != nil && *m.ToolResult.DurationMs == 25
		}},
	} {
		t.Run(name, func(t *testing.T) {
			var m AgentMessage
			if err := json.Unmarshal([]byte(tc.raw), &m); err != nil {
				t.Fatalf("the message is dropped: %v", err)
			}
			if !tc.check(m) {
				t.Fatalf("decoded %+v", m)
			}
		})
	}
}

// JSON.parse keeps a member of any type, so Pi keeps a message whose integer member is a string, a boolean or an array; the field reads as absent and the message is written as it was read.
func TestAgentMessageDecodeKeepsANonNumberWhereAnIntegerIsExpected(t *testing.T) {
	for _, raw := range []string{`{"role":"user","content":"hi","timestamp":"1"}`, `{"role":"user","content":"hi","timestamp":true}`, `{"role":"user","content":"hi","timestamp":[1]}`} {
		var m AgentMessage
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		if m.User == nil || m.User.Timestamp != 0 || m.User.Content != ai.UserText("hi") {
			t.Errorf("%s decoded as %+v", raw, m.User)
		}
		if written, err := json.Marshal(m); err != nil || string(written) != raw {
			t.Errorf("%s written as %s (%v)", raw, written, err)
		}
	}
}
