package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

type testCase struct {
	Name, Provider, Model, Session string
	Length, Window, Cap, MaxTokens int
	History                        bool
	Thinking                       ai.ThinkingLevel
	Headers                        map[string]string
}
type result struct {
	Case          string          `json:"case"`
	MaxCompletion json.RawMessage `json:"maxCompletion"`
	MaxTokens     json.RawMessage `json:"maxTokens"`
	MaxOutput     json.RawMessage `json:"maxOutput"`
	Tools         json.RawMessage `json:"tools"`
	Path          string          `json:"path"`
	Auth          string          `json:"auth"`
	GatewayAuth   string          `json:"gatewayAuth"`
	Affinity      string          `json:"affinity"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	for key, value := range map[string]string{"CLOUDFLARE_API_KEY": "cf-token", "CLOUDFLARE_ACCOUNT_ID": "account-id", "CLOUDFLARE_GATEWAY_ID": "gateway-id"} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	data, err := os.ReadFile("test/parity/testdata/completions-runtime-options.json")
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
	responses := make(chan result, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		responses <- result{test.Name, body["max_completion_tokens"], body["max_tokens"], body["max_output_tokens"], body["tools"], r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("cf-aig-authorization"), r.Header.Get("x-session-affinity")}
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		}
	}))
	defer server.Close()
	generated, ok := ai.LookupModelExact(test.Provider + "/" + test.Model)
	if !ok {
		return fmt.Errorf("missing model %s/%s", test.Provider, test.Model)
	}
	base, err := url.Parse(generated.BaseURL)
	if err != nil {
		return err
	}
	override := map[string]any{}
	if test.Window != 0 {
		override["contextWindow"] = test.Window
	}
	if test.Cap != 0 {
		override["maxTokens"] = test.Cap
	}
	provider := map[string]any{"baseUrl": server.URL + base.Path, "modelOverrides": map[string]any{test.Model: override}}
	if test.Provider == "openai" {
		provider["models"] = []any{map[string]any{"id": test.Model, "name": generated.DisplayName, "api": "openai-completions", "reasoning": generated.Reasoning, "input": generated.Capabilities, "contextWindow": generated.ContextWindow, "maxTokens": generated.MaxOutputTokens}}
	}
	config, err := json.Marshal(map[string]any{"providers": map[string]any{test.Provider: provider}})
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pig-runtime-options-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err = os.WriteFile(filepath.Join(dir, "models.json"), config, 0o600); err != nil {
		return err
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		return err
	}
	model, err := coding.BuildModel(test.Provider+"/"+test.Model, services)
	if err != nil {
		return err
	}
	text := "hi"
	if test.Length > 0 {
		text = strings.Repeat("x", test.Length)
	}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(text)}}}
	if test.History {
		request.Tools = []ai.ToolSchema{}
		request.Messages = []ai.Message{ai.UserMessage{Content: ai.UserText("use the tool")}, ai.AssistantMessage{API: ai.APIOpenAICompletions, Provider: "openai", Model: "gpt-4o-mini", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "t1", Name: "noop", Arguments: ai.JsonObject{}}}}, ai.ToolResultMessage{ToolCallID: "t1", ToolName: "noop", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}}
	}
	options := ai.StreamOptions{MaxTokens: test.MaxTokens, Thinking: test.Thinking, SessionID: test.Session, Headers: ai.ProviderHeadersFromStrings(test.Headers)}
	if test.Provider == "openai" {
		options.APIKey = "test"
	}
	message := services.ModelRuntime().StreamSimple(context.Background(), model, request, options).Result()
	if message.StopReason != ai.StopReasonStop {
		return fmt.Errorf("result=%+v", message)
	}
	return json.NewEncoder(os.Stdout).Encode(<-responses)
}
