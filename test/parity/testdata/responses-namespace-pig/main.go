package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	for _, target := range []struct{ label, model, provider string }{{"same", "gpt-5.4", "openai"}, {"model-switch", "gpt-5.2", "openai"}, {"provider-switch", "gpt-5.4", "azure"}} {
		for _, custom := range []bool{false, true} {
			name, id, args := "lookup", "fc_test", ai.JsonObject{"value": "hello"}
			var tools []ai.ToolSchema
			if custom {
				name, id, args = "query", "ctc_test", ai.JsonObject{"input": "hello"}
				tools = []ai.ToolSchema{{Name: name, Parameters: ai.JsonObject{"type": "object", "properties": ai.JsonObject{"input": ai.JsonObject{"type": "string"}}, "required": []string{"input"}}, ConstrainedSampling: &ai.ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{ai.GrammarFormatOpenAILark: "start: /[a-z]+/"}}}}
			}
			provider := ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{Model: target.model, ProviderID: target.provider, APIKey: "test", Compat: &ai.OpenAIResponsesCompat{SupportsOpenAIGrammarTools: new(true)}})
			captured := errors.New("captured")
			var payload struct {
				Input []struct {
					Type      string `json:"type"`
					ID        string `json:"id"`
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"input"`
			}
			_, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Tools: tools, Messages: []ai.Message{ai.AssistantMessage{API: ai.APIOpenAIResponses, Provider: "openai", Model: "gpt-5.4", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_test|" + id, Name: name, Arguments: args, Namespace: "dynamic_tools"}}}}}), ai.StreamOptions{OnPayload: func(value any, _ *ai.Model) (any, error) {
				data, e := json.Marshal(value)
				if e != nil {
					return nil, e
				}
				if e = json.Unmarshal(data, &payload); e != nil {
					return nil, e
				}
				return nil, captured
			}})
			if !errors.Is(err, captured) {
				return fmt.Errorf("capture: %w", err)
			}
			found := false
			for _, item := range payload.Input {
				if item.Type == "function_call" || item.Type == "custom_tool_call" {
					// Cross-target cases assert namespace removal; native cases also assert replay identity.
					itemID := ""
					if target.label == "same" {
						itemID = item.ID
					}
					fmt.Printf("%s %s %s %q\n", target.label, item.Name, itemID, item.Namespace)
					found = true
				}
			}
			if !found {
				return errors.New("missing replayed tool call")
			}
			if err := provider.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}
