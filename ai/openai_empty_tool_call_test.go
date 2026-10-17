package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// pi-mono #10299: OpenRouter/GLM can stream a tool_calls entry with no id and no
// function name. Pi stores that block verbatim, replays tool_call_id "" for the
// tool result, and every later turn fails with
// "400 tool messages must include a non-empty string tool_call_id".
// pig divergence (D120): the stream gives every tool call an id and
// replay omits calls without a name or id and their results, so the session keeps working.
func TestOpenAICompletionsEmptyToolCallDoesNotWedgeReplay(t *testing.T) {
	for _, tc := range []struct {
		name       string
		toolCall   string
		wantCalls  int
		wantResult int
	}{
		{"no id and no name", `{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}`, 0, 0},
		{"index only", `{"index":0}`, 0, 0},
		{"name without id", `{"index":0,"type":"function","function":{"name":"read","arguments":"{}"}}`, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, body)
				n := len(bodies)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if n == 1 {
					_, _ = w.Write([]byte(`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[` + tc.toolCall + `]}}]}` + "\n\n" +
						`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"))
					return
				}
				_, _ = w.Write([]byte(`data: {"id":"c2","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()
			provider := NewOpenAIProvider(OpenAIConfig{Model: "test-model", APIKey: "test", BaseURL: server.URL})

			messages := []Message{UserMessage{Content: UserText("hi")}}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			first := stream.Result()
			if first.StopReason != StopReasonToolUse {
				t.Fatalf("first turn = %#v", first)
			}
			messages = append(messages, *first)
			for _, block := range first.Content {
				if call, ok := block.(ToolCall); ok {
					messages = append(messages, ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, IsError: true, Content: []ToolResultMessageContent{TextContent{Text: "Tool " + call.Name + " not found"}}})
				}
			}
			messages = append(messages, UserMessage{Content: UserText("again")})
			stream, err = provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if second := stream.Result(); second.StopReason != StopReasonStop {
				t.Fatalf("second turn = %#v", second)
			}
			var request struct {
				Messages []struct {
					Role       string  `json:"role"`
					ToolCallID *string `json:"tool_call_id"`
					ToolCalls  []struct {
						ID       string                `json:"id"`
						Function struct{ Name string } `json:"function"`
					} `json:"tool_calls"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(bodies[1], &request); err != nil {
				t.Fatal(err)
			}
			calls, results := 0, 0
			for _, message := range request.Messages {
				for _, call := range message.ToolCalls {
					calls++
					if call.ID == "" || call.Function.Name == "" {
						t.Errorf("replayed tool call without id or name: %s", bodies[1])
					}
				}
				if message.Role == "tool" {
					results++
					if message.ToolCallID == nil || *message.ToolCallID == "" {
						t.Errorf("tool message without a non-empty tool_call_id: %s", bodies[1])
					}
				}
			}
			if calls != tc.wantCalls || results != tc.wantResult {
				t.Fatalf("replayed calls=%d results=%d, want %d/%d: %s", calls, results, tc.wantCalls, tc.wantResult, strings.TrimSpace(string(bodies[1])))
			}
		})
	}
}
