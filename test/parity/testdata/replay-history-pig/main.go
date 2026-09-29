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

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("test/parity/testdata/replay-history.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Name, Model string
		Messages    []json.RawMessage
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, test := range cases {
			messages := make([]ai.Message, 0, len(test.Messages))
			for _, raw := range test.Messages {
				message, err := decodeHistoryMessage(raw)
				if err != nil {
					return err
				}
				messages = append(messages, message)
			}
			if err := probe(api, test.Name, test.Model, messages); err != nil {
				return err
			}
		}
	}
	return nil
}

// Use the production session decoder, without Agent normalization, to retain the poisoned history for the provider boundary.
func decodeHistoryMessage(raw json.RawMessage) (ai.Message, error) {
	var message agent.AgentMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	switch {
	case message.System != nil:
		return *message.System, nil
	case message.Assistant != nil:
		return message.Assistant.LLMMessage(), nil
	case message.User != nil:
		return message.User.LLMMessage(), nil
	case message.ToolResult != nil:
		r := message.ToolResult
		return ai.ToolResultMessage{ToolCallID: r.ToolCallID, ToolName: r.ToolName, Content: r.Content, IsError: r.IsError, Timestamp: r.Timestamp}, nil
	}
	return nil, fmt.Errorf("unsupported history message")
}
func probe(api ai.API, name, id string, messages []ai.Message) error {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if api == ai.APIOpenAICompletions {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		}
	}))
	defer server.Close()
	metadata := &ai.Model{ID: id, Capabilities: ai.ModelCapabilities{SupportsImages: true}}
	compat := &ai.OpenAICompat{SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolAdditions: new(true), SupportsAdditionalTools: new(true)}
	var provider ai.Provider
	if api == ai.APIOpenAICompletions {
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{Model: id, ModelMetadata: metadata, ProviderID: "moonshotai", BaseURL: server.URL, APIKey: "test", Compat: compat})
	} else {
		provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{Model: id, ModelMetadata: metadata, ProviderID: "openai", BaseURL: server.URL, APIKey: "test", Compat: compat, IsReasoning: true})
	}
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: messages}), ai.StreamOptions{IsReasoning: true})
	if err != nil {
		return err
	}
	if result := stream.Result(); result.StopReason != ai.StopReasonStop {
		return fmt.Errorf("response=%#v", result)
	}
	input := body["input"]
	if api == ai.APIOpenAICompletions {
		input = body["messages"]
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		API   ai.API `json:"api"`
		Case  string `json:"case"`
		Input any    `json:"input"`
	}{api, name, input})
}
