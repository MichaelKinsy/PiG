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
	raw, err := os.ReadFile("test/parity/testdata/provider-error-body/cases.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		return err
	}
	output := map[string][]string{}
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, tc := range cases {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.Status)
				_, _ = w.Write([]byte(tc.Body))
			}))
			var provider ai.Provider
			if api == ai.APIOpenAICompletions {
				provider = ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: "test-model", ProviderID: "openai", BaseURL: server.URL})
			} else {
				provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: "test-model", ProviderID: "openai", BaseURL: server.URL})
			}
			stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{})
			message := ""
			if err != nil {
				message = err.Error()
			} else {
				message = stream.Result().ErrorMessage
			}
			server.Close()
			if message == "" {
				return fmt.Errorf("%s did not surface error", api)
			}
			output[string(api)] = append(output[string(api)], message)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}
