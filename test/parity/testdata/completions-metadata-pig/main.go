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
	data, err := os.ReadFile("test/parity/testdata/completions-metadata.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Name   string            `json:"name"`
		Chunks []json.RawMessage `json:"chunks"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		return err
	}
	for _, test := range cases {
		if err := probe(test.Name, test.Chunks); err != nil {
			return err
		}
	}
	return nil
}

func probe(name string, chunks []json.RawMessage) error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
	}))
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{Model: "openrouter/auto", ProviderID: "openrouter", APIKey: "test", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{})
	if err != nil {
		return err
	}
	result := stream.Result()
	return json.NewEncoder(os.Stdout).Encode(struct {
		Case          string        `json:"case"`
		Model         string        `json:"model"`
		Provider      string        `json:"provider"`
		ResponseModel string        `json:"responseModel"`
		ResponseID    string        `json:"responseId"`
		RawStopReason string        `json:"rawStopReason"`
		StopReason    ai.StopReason `json:"stopReason"`
		ErrorMessage  string        `json:"errorMessage"`
	}{name, result.Model, result.Provider, result.ResponseModel, result.ResponseID, result.RawStopReason, result.StopReason, result.ErrorMessage})
}
