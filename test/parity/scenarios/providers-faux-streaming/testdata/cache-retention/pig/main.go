package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

type cacheCase struct {
	Name      string            `json:"name"`
	API       ai.API            `json:"api"`
	Model     string            `json:"model"`
	Retention ai.CacheRetention `json:"retention"`
	Env       string            `json:"env"`
	Session   string            `json:"session"`
	Proxy     bool              `json:"proxy"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile("test/parity/scenarios/providers-faux-streaming/testdata/cache-retention/cases.json")
	if err != nil {
		return err
	}
	var cases []cacheCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		return err
	}
	results := map[string]any{}
	for _, tc := range cases {
		providerID := "openai"
		if tc.API == ai.APIAnthropicMessages {
			providerID = "anthropic"
		}
		var compat *ai.ModelCompat
		if model, ok := ai.LookupModelExact(providerID + "/" + tc.Model); ok {
			compat = model.Compat
		}
		baseURL := ""
		if tc.Proxy {
			baseURL = "https://my-proxy.example.com/v1"
		}
		var provider ai.Provider
		switch tc.API {
		case ai.APIAnthropicMessages:
			provider = ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "fake-key", Model: tc.Model, ProviderID: providerID, BaseURL: baseURL, Compat: compat})
		case ai.APIOpenAIResponses:
			provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "fake-key", Model: tc.Model, ProviderID: providerID, BaseURL: baseURL, Compat: compat})
		case ai.APIOpenAICompletions:
			provider = ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "fake-key", Model: tc.Model, ProviderID: providerID, BaseURL: baseURL, Compat: compat})
		default:
			return fmt.Errorf("unexpected API %q", tc.API)
		}
		capturedError := errors.New("payload captured")
		var captured map[string]any
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{
			CacheRetention: tc.Retention, SessionID: tc.Session, Env: ai.ProviderEnv{"PI_CACHE_RETENTION": tc.Env},
			OnPayload: func(payload any, _ *ai.Model) (any, error) {
				data, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(data, &captured); err != nil {
					return nil, err
				}
				return nil, capturedError
			},
		})
		if err == nil && stream != nil {
			_ = stream.Result()
		} else if !errors.Is(err, capturedError) {
			return err
		}
		if captured == nil {
			return fmt.Errorf("%s: no payload", tc.Name)
		}
		fields := map[string]any{}
		if tc.API == ai.APIAnthropicMessages {
			fields["system"] = captured["system"].([]any)[0].(map[string]any)["cache_control"]
			messages := captured["messages"].([]any)
			content := messages[len(messages)-1].(map[string]any)["content"]
			var marker any
			if blocks, ok := content.([]any); ok {
				marker = blocks[len(blocks)-1].(map[string]any)["cache_control"]
			}
			fields["user"] = marker
		} else {
			for _, key := range []string{"prompt_cache_key", "prompt_cache_retention", "prompt_cache_options"} {
				fields[key] = captured[key]
			}
		}
		results[tc.Name] = fields
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}
