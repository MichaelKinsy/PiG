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

func main() {
	dir, err := os.MkdirTemp("", "raw-effort-")
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
	for _, api := range []ai.API{ai.APIOpenAIResponses, ai.APIOpenAICompletions} {
		captured := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				panic(err)
			}
			effort, _ := body["reasoning_effort"].(string)
			if reasoning, ok := body["reasoning"].(map[string]any); ok {
				effort, _ = reasoning["effort"].(string)
			}
			captured <- effort
			if effort == "xhigh" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"Unsupported value: xhigh","type":"invalid_request_error"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if api == ai.APIOpenAIResponses {
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			} else {
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}
		}))
		var provider ai.Provider
		if api == ai.APIOpenAIResponses {
			provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: "gpt-5-mini", ProviderID: "openai", IsReasoning: true})
		} else {
			provider = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "gpt-5-mini", ProviderID: "openai", Compat: &ai.OpenAICompat{SupportsReasoningEffort: new(true)}})
		}
		model := &ai.Model{ID: "gpt-5-mini", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: api, Reasoning: true}}
		result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("What is 17 + 23? Think step by step."), Timestamp: 1}}}, ai.StreamOptions{ReasoningEffort: "xhigh"})
		rows = append(rows, []any{api, <-captured, result.StopReason, strings.Contains(result.ErrorMessage, "xhigh")})
		if err := provider.Close(); err != nil {
			panic(err)
		}
		server.Close()
	}
	rows = append(rows, zaiRows(services)...)
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
