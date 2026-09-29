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

type fixture struct {
	Name, Provider, Model, Choice string
	Strip, Tools, Strict          bool
	Thinking, Raw                 ai.ThinkingLevel
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("test/parity/testdata/completions-tool-choice.json")
	if err != nil {
		return err
	}
	var cases []fixture
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	for _, test := range cases {
		if err := probe(test); err != nil {
			return err
		}
	}
	return nil
}
func probe(test fixture) error {
	var body map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer server.Close()
	entry, ok := ai.LookupModelExact(test.Provider + "/" + test.Model)
	if !ok {
		return fmt.Errorf("model %s/%s missing", test.Provider, test.Model)
	}
	model := entry.ToModel()
	model.Capabilities = entry.ToCapabilities()
	model.Capabilities.MaxThinking = ai.ThinkingHigh
	if test.Strip {
		model.ProviderMeta.Compat = nil
	}
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: model.ID, ModelMetadata: model, ProviderID: test.Provider, BaseURL: server.URL, Compat: model.ProviderMeta.Compat})
	defer func() { _ = provider.Close() }()
	request := ai.Context{SystemPrompt: "Follow instructions.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hi")}}}
	if test.Tools {
		var tool ai.ToolSchema
		if err := json.Unmarshal([]byte(`{"name":"test_tool","description":"Test tool","parameters":{"type":"object","properties":{"required":{"type":"string"},"optional":{"type":"number"}},"required":["required"]}}`), &tool); err != nil {
			return err
		}
		if test.Strict {
			tool.ConstrainedSampling = &ai.ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}
		}
		request.Tools = []ai.ToolSchema{tool}
	}
	options := ai.StreamOptions{Thinking: test.Thinking, ReasoningEffort: string(test.Raw), IsReasoning: entry.Reasoning}
	if test.Choice != "" {
		options.ToolChoice = test.Choice
	}
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(request), options)
	if err != nil {
		return err
	}
	result := stream.Result()
	if result.StopReason == ai.StopReasonError {
		return fmt.Errorf("%s: %s", test.Name, result.ErrorMessage)
	}
	var messages []struct{ Role string }
	if err = json.Unmarshal(body["messages"], &messages); err != nil {
		return err
	}
	var tools []struct {
		Function struct {
			Strict     json.RawMessage
			Parameters struct{ Required json.RawMessage }
		}
	}
	if encoded := body["tools"]; encoded != nil {
		if err = json.Unmarshal(encoded, &tools); err != nil {
			return err
		}
	}
	var required, strict json.RawMessage
	if len(tools) > 0 {
		required = tools[0].Function.Parameters.Required
		strict = tools[0].Function.Strict
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Case       string          `json:"case"`
		Choice     json.RawMessage `json:"choice"`
		ToolStream json.RawMessage `json:"toolStream"`
		Thinking   json.RawMessage `json:"thinking"`
		Effort     json.RawMessage `json:"effort"`
		Reasoning  json.RawMessage `json:"reasoning"`
		Role       string          `json:"role"`
		Required   json.RawMessage `json:"required"`
		Strict     json.RawMessage `json:"strict"`
	}{test.Name, body["tool_choice"], body["tool_stream"], body["thinking"], body["reasoning_effort"], body["reasoning"], messages[0].Role, required, strict})
}
