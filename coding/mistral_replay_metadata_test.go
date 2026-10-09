package coding

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestMistralModelRuntimePreservesSelectedReplayMetadata(t *testing.T) {
	for _, vision := range []bool{false, true} {
		name := "nonvision"
		inputs := []string{"text"}
		if vision {
			name = "vision"
			inputs = append(inputs, "image")
		}
		t.Run(name, func(t *testing.T) {
			agentDir := t.TempDir()
			config, err := json.Marshal(map[string]any{"providers": map[string]any{"custom-mistral": map[string]any{"api": "mistral-conversations", "apiKey": "fixture", "baseUrl": "https://example.test", "models": []any{map[string]any{"id": "selected-model", "name": "Selected Model", "input": inputs, "contextWindow": 128000, "maxTokens": 4096}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), config, 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model := services.ModelRuntime().GetModel("custom-mistral", "selected-model")
			if model == nil {
				t.Fatal("configured model missing")
			}
			var wire struct {
				Messages []map[string]any `json:"messages"`
			}
			options := ai.StreamOptions{Fetch: &http.Client{Transport: ai.FetchFunction(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "Bearer fixture" {
					t.Errorf("authorization=%q", request.Header.Get("Authorization"))
				}
				if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
			})}}
			history := []ai.Message{
				ai.AssistantMessage{API: ai.APIAnthropicMessages, Provider: "anthropic", Model: "claude", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "foreign reason"}, ai.ToolCall{ID: "abc-123456", Name: "lookup", Arguments: ai.JsonObject{}}}},
				ai.ToolResultMessage{ToolCallID: "abc-123456", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.ImageContent{Data: "aGVsbG8=", MimeType: "image/png"}}},
			}
			result := services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: history}, options)
			if result.StopReason != ai.StopReasonStop {
				t.Fatal(result)
			}
			if len(wire.Messages) != len(history) {
				t.Fatalf("messages=%+v", wire.Messages)
			}
			content := wire.Messages[0]["content"].([]any)
			if content[0].(map[string]any)["type"] != "text" || content[0].(map[string]any)["text"] != "foreign reason" {
				t.Fatalf("foreign reasoning replay=%v", content)
			}
			if got := wire.Messages[1]["tool_call_id"]; got != "abc123456" {
				t.Fatalf("paired ID=%v", got)
			}
			toolContent := wire.Messages[1]["content"].([]any)
			if vision {
				if len(toolContent) != 2 || toolContent[1].(map[string]any)["image_url"] != "data:image/png;base64,aGVsbG8=" {
					t.Fatalf("vision tool content=%v", toolContent)
				}
			} else if len(toolContent) != 1 || toolContent[0].(map[string]any)["text"] != "(tool image omitted: model does not support images)" {
				t.Fatalf("nonvision tool content=%v", toolContent)
			}
		})
	}
}
