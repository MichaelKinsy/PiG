package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decodeJSONObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestContentJSONMatchesPiTypes(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  map[string]any
	}{
		{
			name:  "text",
			value: TextContent{Text: "answer", TextSignature: "sig"},
			want:  map[string]any{"type": "text", "text": "answer", "textSignature": "sig"},
		},
		{
			name:  "image",
			value: ImageContent{Data: "AAAA", MimeType: "image/png"},
			want:  map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"},
		},
		{
			name:  "thinking",
			value: ThinkingContent{Thinking: "reasoning", ThinkingSignature: "opaque", Redacted: true},
			want: map[string]any{
				"type": "thinking", "thinking": "reasoning", "thinkingSignature": "opaque", "redacted": true,
			},
		},
		{
			name: "tool call",
			value: ToolCall{
				ID: "call", Name: "read", Arguments: JsonObject{"path": "x.go"},
				ThoughtSignature: "thought", Namespace: "functions",
			},
			want: map[string]any{
				"type": "toolCall", "id": "call", "name": "read", "arguments": map[string]any{"path": "x.go"},
				"thoughtSignature": "thought", "namespace": "functions",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := decodeJSONObject(t, test.value); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("content JSON = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestUsageJSONMatchesPiType(t *testing.T) {
	usage := Usage{
		Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, CacheWrite1h: new(5),
		Reasoning: new(6), TotalTokens: 10, Cost: UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
	want := map[string]any{
		"input": float64(1), "output": float64(2), "cacheRead": float64(3), "cacheWrite": float64(4),
		"cacheWrite1h": float64(5), "reasoning": float64(6), "totalTokens": float64(10),
		"cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": float64(1)},
	}
	if got := decodeJSONObject(t, usage); !reflect.DeepEqual(got, want) {
		t.Fatalf("usage JSON = %#v, want %#v", got, want)
	}
}

func TestMessageJSONMatchesPiRoles(t *testing.T) {
	messages := []struct {
		name string
		msg  Message
		role string
	}{
		{name: "system", msg: SystemMessage{Content: SystemText("prompt"), Timestamp: 1}, role: "system"},
		{name: "user", msg: UserMessage{Content: UserText("question"), Timestamp: 2}, role: "user"},
		{name: "assistant", msg: AssistantMessage{
			Content: []AssistantContentBlock{TextContent{Text: "answer"}}, API: APIOpenAIResponses,
			Provider: "openai", Model: "gpt", Usage: Usage{Cost: UsageCost{}}, StopReason: StopReasonStop, Timestamp: 3,
		}, role: "assistant"},
		{name: "tool result", msg: ToolResultMessage{
			ToolCallID: "call", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "done"}}, Timestamp: 4,
		}, role: "toolResult"},
	}
	for _, test := range messages {
		t.Run(test.name, func(t *testing.T) {
			got := decodeJSONObject(t, test.msg)
			if got["role"] != test.role {
				t.Fatalf("role = %#v, want %q", got["role"], test.role)
			}
		})
	}
}
