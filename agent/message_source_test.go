package agent

import (
	"encoding/json"
	"testing"
)

// Pi holds the object JSON.parse returned for a session line or an extension result and writes it with JSON.stringify, so its member order, members PiG does not model and its spellings reach every writer. The expected texts are node's JSON.stringify(JSON.parse(line)).
func TestDecodedMessageWritesTheObjectItWasDecodedFrom(t *testing.T) {
	cases := []struct {
		name, line, want string
	}{
		{
			name: "user members in another order",
			line: `{"timestamp":1767225601000,"content":[{"text":"hi <b> & \u00e9","type":"text"}],"role":"user"}`,
			want: `{"timestamp":1767225601000,"content":[{"text":"hi <b> & é","type":"text"}],"role":"user"}`,
		},
		{
			name: "assistant with an extra member, an empty errorMessage and an exponent",
			line: `{"role":"assistant","content":[{"type":"text","text":"a\u2028b"},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"x","limit":1e2}}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1767225602000,"unread":true,"errorMessage":""}`,
			want: "{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"a\u2028b\"},{\"type\":\"toolCall\",\"id\":\"c1\",\"name\":\"read\",\"arguments\":{\"path\":\"x\",\"limit\":100}}],\"api\":\"openai-completions\",\"provider\":\"p\",\"model\":\"m\",\"usage\":{\"input\":1,\"output\":2,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":3,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}},\"stopReason\":\"toolUse\",\"timestamp\":1767225602000,\"unread\":true,\"errorMessage\":\"\"}",
		},
		{
			name: "tool result with isError before content",
			line: `{"role":"toolResult","toolCallId":"c1","toolName":"read","isError":false,"content":[{"type":"text","text":"ok"}],"details":{"b":1,"a":2},"timestamp":1767225603000}`,
			want: `{"role":"toolResult","toolCallId":"c1","toolName":"read","isError":false,"content":[{"type":"text","text":"ok"}],"details":{"b":1,"a":2},"timestamp":1767225603000}`,
		},
		{
			name: "bash execution with timestamp first",
			line: `{"timestamp":1767225604000,"role":"bashExecution","command":"ls","output":"x","exitCode":0,"cancelled":false,"truncated":false}`,
			want: `{"timestamp":1767225604000,"role":"bashExecution","command":"ls","output":"x","exitCode":0,"cancelled":false,"truncated":false}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var message AgentMessage
			if err := json.Unmarshal([]byte(tc.line), &message); err != nil {
				t.Fatal(err)
			}
			got, err := message.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("MarshalJSON:\n got %s\nwant %s", got, tc.want)
			}
			source, err := message.SourceJSON()
			if err != nil || string(source) != tc.want {
				t.Fatalf("SourceJSON = %s, %v; want %s", source, err, tc.want)
			}
			if clone, _ := message.Clone().MarshalJSON(); string(clone) != tc.want {
				t.Fatalf("Clone().MarshalJSON:\n got %s\nwant %s", clone, tc.want)
			}
		})
	}
}

// A message in the order Pi writes encodes to the same text from its fields, so it retains none.
func TestDecodedMessageInPiOrderRetainsNoText(t *testing.T) {
	for _, line := range []string{
		`{"role":"user","content":[{"type":"text","text":"pi order"}],"timestamp":1767225605000}`,
		`{"role":"user","content":"plain","timestamp":1767225605000}`,
		`{"role":"assistant","content":[{"type":"text","text":"x"}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1767225602000}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":1767225603000}`,
		`{"role":"bashExecution","command":"ls","output":"x","exitCode":0,"cancelled":false,"truncated":false,"timestamp":1767225604000}`,
	} {
		var message AgentMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if source, err := message.SourceJSON(); source != nil || err != nil {
			t.Errorf("%s retained %s, %v", line, source, err)
		}
		if got, _ := message.MarshalJSON(); string(got) != line {
			t.Errorf("MarshalJSON = %s, want %s", got, line)
		}
	}
}

// agent-session.ts normalizes a handler result without content with { ...replacement, content: [] }: an existing member keeps its place and a missing one follows the others.
func TestWithEmptyContentSpreadsContent(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`{"timestamp":1,"content":null,"role":"user"}`, `{"timestamp":1,"content":[],"role":"user"}`},
		{`{"timestamp":1,"role":"user"}`, `{"timestamp":1,"role":"user","content":[]}`},
		{`{"role":"toolResult","toolCallId":"c","toolName":"t","isError":false,"timestamp":1}`, `{"role":"toolResult","toolCallId":"c","toolName":"t","isError":false,"timestamp":1,"content":[]}`},
		{`{"role":"custom","customType":"k","timestamp":1,"display":true}`, `{"role":"custom","customType":"k","timestamp":1,"display":true,"content":[]}`},
	} {
		var message AgentMessage
		if err := json.Unmarshal([]byte(tc.line), &message); err != nil {
			t.Fatal(err)
		}
		got, err := message.WithEmptyContent().MarshalJSON()
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: got %s, %v; want %s", tc.line, got, err, tc.want)
		}
	}
}
