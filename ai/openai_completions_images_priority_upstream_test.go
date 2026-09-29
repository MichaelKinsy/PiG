package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestOpenAICompletionsToolResultImagesUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-result-images.test.ts:73
	t.Run("omits empty text parts from user messages with images", func(t *testing.T) {
		messages, err := convertMessages([]Message{UserMessage{Content: UserContentBlocks{TextContent{}, ImageContent{Data: "ZmFrZQ==", MimeType: "image/png"}}}}, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertCompletionsJSON(t, messages, `[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,ZmFrZQ=="}}]}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-result-images.test.ts:101
	t.Run("batches tool-result images after consecutive tool results", func(t *testing.T) {
		history := []Message{UserMessage{Content: UserText("Read the images")}, AssistantMessage{API: APIOpenAICompletions, Provider: "openai", Model: "gpt-4o-mini", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{
			ToolCall{ID: "tool-1", Name: "read", Arguments: map[string]any{"path": "img-1.png"}},
			ToolCall{ID: "tool-2", Name: "read", Arguments: map[string]any{"path": "img-2.png"}},
		}}}
		for _, id := range []string{"tool-1", "tool-2"} {
			history = append(history, ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "Read image file [image/png]"}, ImageContent{Data: "ZmFrZQ==", MimeType: "image/png"}}})
		}
		messages, err := convertMessages(history, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		var roles []string
		for _, message := range messages {
			roles = append(roles, message.Role)
		}
		if !reflect.DeepEqual(roles, []string{"user", "assistant", "tool", "tool", "user"}) {
			t.Fatalf("roles=%v", roles)
		}
		parts, ok := messages[4].Content.([]oaiContentPart)
		if !ok {
			t.Fatalf("content=%#v", messages[4].Content)
		}
		images := 0
		for _, part := range parts {
			if part.Type == "image_url" {
				images++
			}
		}
		if images != 2 {
			t.Fatalf("images=%d", images)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-result-images.test.ts:147
	t.Run("uses '(no tool output)' placeholder for empty tool results without images", func(t *testing.T) {
		messages, err := convertMessages([]Message{UserMessage{Content: UserText("Run the command")}, AssistantMessage{API: APIOpenAICompletions, Provider: "openai", Model: "gpt-4o-mini", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "tool-1", Name: "bash", Arguments: map[string]any{"command": "true"}}}}, ToolResultMessage{ToolCallID: "tool-1", ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: ""}}}}, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 3 || messages[2].Role != "tool" || messages[2].Content != "(no tool output)" {
			t.Fatalf("messages=%#v", messages)
		}
	})
}

func assertCompletionsJSON(t *testing.T, value any, want string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(data, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("got %s; want %s", data, want)
	}
}

func TestOpenAICompletionsVLLMPriorityUpstream(t *testing.T) {
	for _, tc := range []struct {
		name     string
		priority *float64
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-vllm-priority.test.ts:80
		{"sends compat.vllmPriority as the top-level priority request field", new(float64(10))},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-vllm-priority.test.ts:88
		{"omits priority when vllmPriority is not set", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payloads := make(chan map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				payloads <- payload
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}\n\n")
			}))
			defer server.Close()
			provider := NewOpenAIProvider(OpenAIConfig{Model: "gpt-4o-mini", APIKey: "test-key", BaseURL: server.URL, Compat: &OpenAICompat{VLLMPriority: tc.priority}})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "sys", Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatal(result)
			}
			payload := <-payloads
			if tc.priority == nil {
				if _, ok := payload["priority"]; ok {
					t.Fatalf("priority present: %s", payload["priority"])
				}
			} else if strings.TrimSpace(string(payload["priority"])) != "10" {
				t.Fatalf("priority=%s", payload["priority"])
			}
		})
	}
}
