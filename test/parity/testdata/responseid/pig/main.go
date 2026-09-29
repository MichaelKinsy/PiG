package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var results []map[string]string
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		body, err := os.ReadFile("test/parity/testdata/responseid/" + string(api) + ".sse")
		if err != nil {
			return err
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(body)
		}))
		var provider ai.Provider
		if api == ai.APIOpenAICompletions {
			provider = ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: "requested", BaseURL: server.URL})
		} else {
			provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: "requested", BaseURL: server.URL})
		}
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{SystemPrompt: "You are a helpful assistant. Be concise.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Reply with exactly: response id test")}}}), ai.StreamOptions{})
		if err != nil {
			server.Close()
			return err
		}
		response := stream.Result()
		server.Close()
		if response.StopReason == ai.StopReasonError {
			return fmt.Errorf("%s", response.ErrorMessage)
		}
		results = append(results, map[string]string{"api": string(api), "responseId": response.ResponseID, "responseModel": response.ResponseModel})
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}
