package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

type transport struct {
	name      string
	reasoning bool
	compat    *ai.OpenAICompat
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	data, err := os.ReadFile("test/parity/testdata/responses-tool-notices.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Name     string
		Messages []json.RawMessage
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	transports := []transport{
		{"additional_tools", true, &ai.OpenAICompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}},
		{"additional_tools-system-role", false, &ai.OpenAICompat{SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true)}},
		{"tool-search", true, &ai.OpenAICompat{SupportsMidConvoSystemMessages: new(true), SupportsToolSearch: new(true)}},
		{"folded", true, &ai.OpenAICompat{}},
	}
	for _, test := range cases {
		messages := make([]ai.Message, 0, len(test.Messages))
		for _, raw := range test.Messages {
			var message agent.AgentMessage
			if err := json.Unmarshal(raw, &message); err != nil {
				return err
			}
			switch {
			case message.System != nil:
				messages = append(messages, *message.System)
			case message.User != nil:
				messages = append(messages, message.User.LLMMessage())
			default:
				return fmt.Errorf("unsupported history message in %s", test.Name)
			}
		}
		for _, transport := range transports {
			if err := probe(test.Name, transport, messages); err != nil {
				return err
			}
		}
	}
	return nil
}

// probe sends the transcript through the Responses provider's request path and prints the request's input.
func probe(name string, transport transport, messages []ai.Message) error {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	metadata := &ai.Model{ID: "gpt-test"}
	provider := ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{Model: "gpt-test", ModelMetadata: metadata, ProviderID: "openai", BaseURL: server.URL, APIKey: "test", Compat: transport.compat, IsReasoning: transport.reasoning})
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: messages}), ai.StreamOptions{IsReasoning: transport.reasoning})
	if err != nil {
		return err
	}
	if result := stream.Result(); result.StopReason != ai.StopReasonStop {
		return fmt.Errorf("%s %s: response=%#v", name, transport.name, result)
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Case      string `json:"case"`
		Transport string `json:"transport"`
		Input     any    `json:"input"`
	}{name, transport.name, body["input"]})
}
