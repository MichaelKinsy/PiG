package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func findText(value any) string {
	switch value := value.(type) {
	case string:
		if strings.Contains(value, "Text with unpaired surrogate:") {
			return value
		}
	case []any:
		for _, item := range value {
			if text := findText(item); text != "" {
				return text
			}
		}
	case map[string]any:
		for _, item := range value {
			if text := findText(item); text != "" {
				return text
			}
		}
	}
	return ""
}
func main() {
	dir, err := os.MkdirTemp("", "unicode-provider-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		panic(err)
	}
	rows := [][]any{}
	for _, tc := range []struct {
		api             ai.API
		provider, model string
	}{
		{ai.APIOpenAICompletions, "openai", "gpt-4o-mini"},
		{ai.APIOpenAIResponses, "openai", "gpt-5-mini"},
		{ai.APIAnthropicMessages, "anthropic", "claude-haiku-4-5"},
		{ai.APIGoogleGenerativeAI, "google", "gemini-2.5-flash"},
		{ai.APIMistralConversations, "mistral", "devstral-medium-latest"},
	} {
		captured := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				panic(err)
			}
			captured <- findText(body)
			w.Header().Set("Content-Type", "text/event-stream")
			reply := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			switch tc.api {
			case ai.APIOpenAIResponses:
				reply = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
			case ai.APIAnthropicMessages:
				reply = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":{\"input_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			case ai.APIGoogleGenerativeAI:
				reply = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"
			}
			_, _ = io.WriteString(w, reply)
		}))
		var provider ai.Provider
		switch tc.api {
		case ai.APIOpenAICompletions:
			provider = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: tc.model, ProviderID: tc.provider})
		case ai.APIOpenAIResponses:
			provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: tc.model, ProviderID: tc.provider})
		case ai.APIAnthropicMessages:
			provider = ai.NewAnthropicProvider(ai.AnthropicConfig{BaseURL: server.URL, APIKey: "test", Model: tc.model, ProviderID: tc.provider})
		case ai.APIGoogleGenerativeAI:
			provider = ai.NewGoogleProvider(ai.GoogleConfig{BaseURL: server.URL, APIKey: "test", Model: tc.model, ProviderID: tc.provider})
		case ai.APIMistralConversations:
			provider = ai.NewMistralProvider(ai.MistralConfig{BaseURL: server.URL, APIKey: "test", Model: tc.model, ProviderID: tc.provider})
		}
		id := "test_2"
		if tc.provider == "mistral" {
			id = "testtool2"
		}
		text := "Text with unpaired surrogate: \xed\xa0\xbd <- should be sanitized"
		request := ai.Context{SystemPrompt: "You are a helpful assistant.", Tools: []ai.ToolSchema{{Name: "test_tool", Description: "A test tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}, Messages: []ai.Message{
			ai.UserMessage{Content: ai.UserText("Use the test tool"), Timestamp: 1},
			ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: "test_tool", Arguments: ai.JsonObject{}}}, API: tc.api, Provider: tc.provider, Model: tc.model, StopReason: ai.StopReasonToolUse, Timestamp: 2},
			ai.ToolResultMessage{ToolCallID: id, ToolName: "test_tool", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Timestamp: 3},
			ai.UserMessage{Content: ai.UserText("What did the tool return?"), Timestamp: 4},
		}}
		result := services.ModelRuntime().Complete(context.Background(), &ai.Model{ID: tc.model, Provider: provider, ProviderMeta: ai.ProviderMetadata{API: tc.api}}, request, ai.StreamOptions{})
		rows = append(rows, []any{tc.api, <-captured, result.StopReason})
		if request.Messages[2].(ai.ToolResultMessage).Content[0].(ai.TextContent).Text != text {
			panic("provider mutated history")
		}
		if err := provider.Close(); err != nil {
			panic(err)
		}
		server.Close()
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(rows); err != nil {
		panic(err)
	}
}
