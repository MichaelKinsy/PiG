package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("ai/testdata/upstream-red-circle.png")
	if err != nil {
		return err
	}
	image := base64.StdEncoding.EncodeToString(data)
	results := map[string]any{}
	for _, spec := range []struct {
		api             ai.API
		provider, model string
	}{{ai.APIGoogleGenerativeAI, "google", "gemini-2.5-flash"}, {ai.APIOpenAICompletions, "openrouter", "z-ai/glm-4.5v"}, {ai.APIMistralConversations, "mistral", "pixtral-12b"}, {ai.APIOpenAICompletions, "openai", "gpt-4o-mini"}, {ai.APIOpenAIResponses, "openai", "gpt-5-mini"}} {
		for _, mixed := range []bool{false, true} {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					panic(err)
				}
				requests <- body
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"fixture rejection"}}`))
			}))
			var provider ai.Provider
			switch spec.api {
			case ai.APIGoogleGenerativeAI:
				provider = ai.NewGoogleProvider(ai.GoogleConfig{APIKey: "test", Model: spec.model, ProviderID: spec.provider, BaseURL: server.URL})
			case ai.APIOpenAICompletions:
				provider = ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: spec.model, ProviderID: spec.provider, BaseURL: server.URL})
			case ai.APIMistralConversations:
				provider = ai.NewMistralProvider(ai.MistralConfig{APIKey: "test", Model: spec.model, ProviderID: spec.provider, BaseURL: server.URL})
			case ai.APIOpenAIResponses:
				provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: spec.model, ProviderID: spec.provider, BaseURL: server.URL})
			}
			content := []ai.ToolResultMessageContent{}
			if mixed {
				content = append(content, ai.TextContent{Text: "diameter of 100 pixels"})
			}
			content = append(content, ai.ImageContent{Data: image, MimeType: "image/png"})
			stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.AssistantMessage{API: spec.api, Provider: spec.provider, Model: spec.model, StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "imagecall", Name: "get_circle", Arguments: ai.JsonObject{}}}}, ai.ToolResultMessage{ToolCallID: "imagecall", ToolName: "get_circle", Content: content}}}), ai.StreamOptions{})
			// pi.mjs awaits the stream's result; the fixture's HTTP 400 surfaces there.
			if err == nil && stream.Result().StopReason != ai.StopReasonError {
				server.Close()
				return fmt.Errorf("expected fixture rejection")
			}
			server.Close()
			request := <-requests
			images := []string{}
			var visit func(any)
			visit = func(value any) {
				switch value := value.(type) {
				case map[string]any:
					if inline, ok := value["inlineData"].(map[string]any); ok {
						images = append(images, "data:"+inline["mimeType"].(string)+";base64,"+inline["data"].(string))
					}
					if imageURL, ok := value["image_url"].(string); ok {
						images = append(images, imageURL)
					} else if imageURL, ok := value["image_url"].(map[string]any); ok {
						images = append(images, imageURL["url"].(string))
					}
					for _, key := range slices.Sorted(maps.Keys(value)) {
						visit(value[key])
					}
				case []any:
					for _, item := range value {
						visit(item)
					}
				}
			}
			visit(request)
			encoded, _ := json.Marshal(request)
			results[fmt.Sprintf("%s/%s/%v", spec.provider, spec.api, mixed)] = map[string]any{"images": images, "hasDescription": strings.Contains(string(encoded), "diameter of 100 pixels")}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}
