package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

type testCase struct {
	Name      string
	Compat    map[string]any
	Level     ai.ThinkingLevel
	Budgets   *ai.ThinkingBudgets
	MaxTokens int
}
type output struct {
	Case        string          `json:"case"`
	Budget      json.RawMessage `json:"budget"`
	Alias       json.RawMessage `json:"alias"`
	AliasTokens json.RawMessage `json:"aliasTokens"`
	Kwargs      json.RawMessage `json:"kwargs"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("test/parity/testdata/completions-thinking-budgets.json")
	if err != nil {
		return err
	}
	var cases []testCase
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	for _, test := range cases {
		if err = probe(test); err != nil {
			return err
		}
	}
	return nil
}
func probe(test testCase) error {
	requests := make(chan output, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- output{test.Name, body["thinking_token_budget"], body["thinking_budget"], body["thinking_budget_tokens"], body["chat_template_kwargs"]}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer server.Close()
	definition := map[string]any{"id": "zai-org/glm-5.2", "name": "GLM 5.2 (local vLLM)", "api": "openai-completions", "reasoning": true, "input": []string{"text"}, "contextWindow": 262144, "maxTokens": 16384, "compat": test.Compat}
	data, err := json.Marshal(map[string]any{"providers": map[string]any{"local-vllm": map[string]any{"api": "openai-completions", "baseUrl": server.URL + "/v1", "models": []any{definition}}}})
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pig-thinking-budget-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err = os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
		return err
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		return err
	}
	model, err := coding.BuildModel("local-vllm/zai-org/glm-5.2", services)
	if err != nil {
		return err
	}
	result := services.ModelRuntime().StreamSimple(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hi")}}}, ai.StreamOptions{APIKey: "test", Thinking: test.Level, ThinkingBudgets: test.Budgets, MaxTokens: test.MaxTokens}).Result()
	if result.StopReason != ai.StopReasonStop {
		return fmt.Errorf("result=%+v", result)
	}
	return json.NewEncoder(os.Stdout).Encode(<-requests)
}
