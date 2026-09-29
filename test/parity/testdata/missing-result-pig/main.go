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
		if strings.Contains(value, "No result provided") {
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
	dir, err := os.MkdirTemp("", "missing-tool-result-")
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
		type observation struct {
			text    string
			found   bool
			content any
		}
		captured := make(chan observation, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				panic(err)
			}
			seen := observation{text: findText(body)}
			if tc.api == ai.APIOpenAICompletions {
				object, _ := body.(map[string]any)
				messages, _ := object["messages"].([]any)
				for _, item := range messages {
					message, _ := item.(map[string]any)
					if message["role"] == "assistant" && message["tool_calls"] != nil {
						seen.found, seen.content = true, message["content"]
					}
				}
			}
			captured <- seen
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
		request := ai.Context{SystemPrompt: "You are a helpful assistant. Use the calculate tool when asked to perform calculations.", Tools: []ai.ToolSchema{{Name: "calculate", Description: "Evaluate mathematical expressions", Parameters: map[string]any{"type": "object", "properties": map[string]any{"expression": map[string]any{"type": "string", "description": "The mathematical expression to evaluate"}}, "required": []string{"expression"}}}}, Messages: []ai.Message{
			ai.UserMessage{Content: ai.UserText("Please calculate 25 * 18 using the calculate tool."), Timestamp: 1},
			ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "calc00001", Name: "calculate", Arguments: ai.JsonObject{"expression": "25 * 18"}}}, API: tc.api, Provider: tc.provider, Model: tc.model, StopReason: ai.StopReasonToolUse, Timestamp: 2},
			ai.UserMessage{Content: ai.UserText("Never mind, just tell me what is 2+2?"), Timestamp: 4},
		}}
		result := services.ModelRuntime().Complete(context.Background(), &ai.Model{ID: tc.model, Provider: provider, ProviderMeta: ai.ProviderMetadata{API: tc.api}}, request, ai.StreamOptions{})
		seen := <-captured
		rows = append(rows, []any{tc.api, seen.text, result.StopReason, seen.found, seen.content})
		if len(request.Messages) != 3 {
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
