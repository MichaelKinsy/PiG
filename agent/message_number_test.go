package agent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// Pi holds a session message as JSON.parse returns it, so a number member keeps the message whatever its spelling: an integral value spelled with a fraction or an exponent (Python writes 1767225603000.0), a fractional value, or one beyond int64. The message is written as JSON.stringify writes the parsed object. Expectations: node -e 'console.log(JSON.stringify(JSON.parse(text)))' with Node 24.
func TestDecodedMessageKeepsNumbersInAnySpelling(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		check            func(t *testing.T, m AgentMessage)
	}{
		{
			name: "exponent timestamp",
			text: `{"role":"user","content":"hi","timestamp":1.767225603e12}`,
			want: `{"role":"user","content":"hi","timestamp":1767225603000}`,
			check: func(t *testing.T, m AgentMessage) {
				if m.User.Timestamp != 1767225603000 {
					t.Errorf("timestamp=%d", m.User.Timestamp)
				}
			},
		},
		{
			name: "fraction-spelled timestamp",
			text: `{"role":"user","content":[{"type":"text","text":"x"}],"timestamp":1767225603000.0}`,
			want: `{"role":"user","content":[{"type":"text","text":"x"}],"timestamp":1767225603000}`,
			check: func(t *testing.T, m AgentMessage) {
				if m.User.Timestamp != 1767225603000 {
					t.Errorf("timestamp=%d", m.User.Timestamp)
				}
			},
		},
		{
			name: "assistant usage, duration, diagnostics and deferred handle",
			text: `{"role":"assistant","content":[{"type":"text","text":"ok"}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1e1,"output":2.0,"cacheRead":0.0,"cacheWrite":0,"totalTokens":12.0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":17.0e11,"durationMs":1.5e3,"diagnostics":[{"type":"retry","timestamp":2.0e3}],"deferred":{"provider":"p","modelId":"m","api":"openai-completions","id":"d","expiresAt":3e3,"pollAfterMs":5.0e2}}`,
			want: `{"role":"assistant","content":[{"type":"text","text":"ok"}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1700000000000,"durationMs":1500,"diagnostics":[{"type":"retry","timestamp":2000}],"deferred":{"provider":"p","modelId":"m","api":"openai-completions","id":"d","expiresAt":3000,"pollAfterMs":500}}`,
			check: func(t *testing.T, m AgentMessage) {
				a := m.Assistant
				if a.Timestamp != 1700000000000 || a.Usage.Input != 10 || a.Usage.Output != 2 || a.Usage.TotalTokens != 12 {
					t.Errorf("timestamp=%d usage=%+v", a.Timestamp, a.Usage)
				}
				if a.DurationMs == nil || *a.DurationMs != 1500 {
					t.Errorf("durationMs=%v", a.DurationMs)
				}
				if len(a.Diagnostics) != 1 || a.Diagnostics[0].Timestamp != 2000 {
					t.Errorf("diagnostics=%+v", a.Diagnostics)
				}
				if a.Deferred == nil || a.Deferred.ExpiresAt == nil || *a.Deferred.ExpiresAt != 3000 || a.Deferred.PollAfterMS == nil || *a.Deferred.PollAfterMS != 500 {
					t.Errorf("deferred=%+v", a.Deferred)
				}
			},
		},
		{
			name: "tool result nested calls",
			text: `{"role":"toolResult","toolCallId":"c","toolName":"bash","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":1.0e3,"nestedCalls":{"calls":[{"id":"n","name":"read","status":"done","argumentsBytes":4.0e0,"durationMs":7e0}],"complete":true}}`,
			want: `{"role":"toolResult","toolCallId":"c","toolName":"bash","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":1000,"nestedCalls":{"calls":[{"id":"n","name":"read","status":"done","argumentsBytes":4,"durationMs":7}],"complete":true}}`,
			check: func(t *testing.T, m AgentMessage) {
				r := m.ToolResult
				if r.Timestamp != 1000 || r.NestedCalls == nil || len(r.NestedCalls.Calls) != 1 {
					t.Fatalf("timestamp=%d nested=%+v", r.Timestamp, r.NestedCalls)
				}
				call := r.NestedCalls.Calls[0]
				if call.ArgumentsBytes == nil || *call.ArgumentsBytes != 4 || call.DurationMs == nil || *call.DurationMs != 7 {
					t.Errorf("nested call=%+v", call)
				}
			},
		},
		{
			name: "fraction in a nested record",
			text: `{"role":"toolResult","toolCallId":"c","toolName":"bash","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":1000,"nestedCalls":{"calls":[{"id":"n","name":"read","status":"done","argumentsBytes":4.5,"durationMs":7e0}],"complete":true}}`,
			want: `{"role":"toolResult","toolCallId":"c","toolName":"bash","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":1000,"nestedCalls":{"calls":[{"id":"n","name":"read","status":"done","argumentsBytes":4.5,"durationMs":7}],"complete":true}}`,
			check: func(t *testing.T, m AgentMessage) {
				if m.ToolResult.Timestamp != 1000 || m.ToolResult.ToolName != "bash" {
					t.Errorf("members beside the fraction: %+v", m.ToolResult)
				}
			},
		},
		{
			name: "fractional timestamp",
			text: `{"role":"user","content":"hi","timestamp":1767225603000.5}`,
			want: `{"role":"user","content":"hi","timestamp":1767225603000.5}`,
		},
		{
			name: "fractional usage and a timestamp beyond int64",
			text: `{"role":"assistant","content":[],"api":"openai-completions","provider":"p","model":"m","usage":{"input":10.5,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12.5,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1e300}`,
			want: `{"role":"assistant","content":[],"api":"openai-completions","provider":"p","model":"m","usage":{"input":10.5,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12.5,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1e+300}`,
			check: func(t *testing.T, m AgentMessage) {
				if m.Assistant.Usage.Output != 2 || m.Assistant.ModelID != "m" {
					t.Errorf("members after the fraction: usage=%+v model=%q", m.Assistant.Usage, m.Assistant.ModelID)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m AgentMessage
			if err := json.Unmarshal([]byte(tc.text), &m); err != nil {
				t.Fatalf("Pi keeps the message; PiG: %v", err)
			}
			encoded, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			got, err := jsonstringify.Canonicalize(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("written\n got %s\nwant %s", got, tc.want)
			}
			if tc.check != nil {
				tc.check(t, m)
			}
		})
	}
}

// A message keeps its own copy of the text it was decoded from: a decoder reuses its line buffer.
func TestDecodedMessageOwnsItsSourceText(t *testing.T) {
	const text = `{"timestamp":1,"content":"hi","role":"user"}`
	buffer := []byte(text)
	var m AgentMessage
	if err := json.Unmarshal(buffer, &m); err != nil {
		t.Fatal(err)
	}
	for i := range buffer {
		buffer[i] = ' '
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != text {
		t.Fatalf("written %s want %s", encoded, text)
	}
}
