package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func main() {
	root, err := os.MkdirTemp("", "anthropic-metadata-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			panic(err)
		}
	}()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: root, AgentDir: root})
	if err != nil {
		panic(err)
	}
	for _, row := range []struct{ provider, key string }{{"fireworks", "test-fireworks-key"}, {"anthropic", "sk-ant-oat01-test"}, {"custom-messages", "test-custom-key"}} {
		model := &ai.Model{ID: "new-reasoner", DisplayName: "New Reasoner", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: row.provider, API: ai.APIAnthropicMessages, BaseURL: "http://127.0.0.1:9", Reasoning: true, Compat: &ai.ModelCompat{ForceAdaptiveThinking: new(true)}}, Capabilities: ai.ModelCapabilities{ContextWindow: 32768, MaxOutputTokens: 12345}, ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingMax: new("low")}}
		var captured map[string]json.RawMessage
		ctx := extension.WithModelStreamRequest(context.Background(), extension.ModelStreamRequest{API: true})
		result := services.ModelRuntime().CompleteSimple(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("test"), Timestamp: 0}}}, ai.StreamOptions{APIKey: row.key, Thinking: ai.ThinkingLevelMax, OnPayload: func(value any, _ *ai.Model) (any, error) {
			data, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(data, &captured); err != nil {
				return nil, err
			}
			return nil, errors.New("payload captured")
		}})
		if captured == nil || result.StopReason != ai.StopReasonError || result.ErrorMessage != "payload captured" {
			panic(result)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"provider": row.provider, "thinking": captured["thinking"], "output_config": captured["output_config"], "max_tokens": captured["max_tokens"]}); err != nil {
			panic(err)
		}
	}
}
