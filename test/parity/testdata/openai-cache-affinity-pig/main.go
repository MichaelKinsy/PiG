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

type probeCase struct {
	Name, Path, Session   string
	Retention             ai.CacheRetention
	Compat                *ai.OpenAICompat
	Headers, ModelHeaders map[string]string
}
type probeResult struct {
	API       string `json:"api"`
	Case      string `json:"case"`
	Key       string `json:"key"`
	Retention string `json:"retention"`
	Session   string `json:"session"`
	Client    string `json:"client"`
	Affinity  string `json:"affinity"`
	Router    string `json:"router"`
	Reasoning any    `json:"reasoning"`
	Choice    string `json:"choice"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("test/parity/testdata/openai-cache-affinity.json")
	if err != nil {
		return err
	}
	var cases []probeCase
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	for _, responses := range []bool{false, true} {
		for _, test := range cases {
			if err = probe(responses, test); err != nil {
				return err
			}
		}
	}
	return nil
}
func probe(responses bool, test probeCase) error {
	requests := make(chan probeResult, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key       string `json:"prompt_cache_key"`
			Retention string `json:"prompt_cache_retention"`
			Reasoning any    `json:"reasoning"`
			Choice    string `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		api := "completions"
		if responses {
			api = "responses"
		}
		requests <- probeResult{api, test.Name, body.Key, body.Retention, r.Header.Get("session_id"), r.Header.Get("x-client-request-id"), r.Header.Get("x-session-affinity"), r.Header.Get("x-session-id"), body.Reasoning, body.Choice}
		w.Header().Set("Content-Type", "text/event-stream")
		if responses {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		}
	}))
	defer server.Close()
	var provider ai.Provider
	request := ai.Context{SystemPrompt: "sys", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}
	options := ai.StreamOptions{SessionID: test.Session, CacheRetention: test.Retention, Headers: ai.ProviderHeadersFromStrings(test.Headers), Env: ai.ProviderEnv{"PI_CACHE_RETENTION": ""}}
	if responses {
		provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{Model: "gpt-5-mini", ProviderID: "openai", APIKey: "test-key", BaseURL: server.URL + test.Path, Compat: test.Compat, ExtraHeaders: test.ModelHeaders, IsReasoning: true})
		options.ToolChoice = "required"
		request.Tools = []ai.ToolSchema{{Name: "ping", Description: "Ping", Parameters: ai.JsonObject{"type": "object", "properties": ai.JsonObject{"value": ai.JsonObject{"type": "string"}}, "required": []string{"value"}}}}
	} else {
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{Model: "gpt-4o-mini", ProviderID: "openai", APIKey: "test-key", BaseURL: server.URL + test.Path, Compat: test.Compat, ExtraHeaders: test.ModelHeaders})
	}
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(request), options)
	if err != nil {
		return err
	}
	if result := stream.Result(); result.StopReason != ai.StopReasonStop {
		return fmt.Errorf("result=%+v", result)
	}
	return json.NewEncoder(os.Stdout).Encode(<-requests)
}
