package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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
	var results bytes.Buffer
	results.WriteByte('{')
	for i, tc := range cases {
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
		var captured *orderedjson.Object
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{
			CacheRetention: tc.Retention, SessionID: tc.Session, Env: ai.ProviderEnv{"PI_CACHE_RETENTION": tc.Env},
			OnPayload: func(payload any, _ *ai.Model) (any, error) {
				// The bytes an extension's before_provider_request handler is handed: Pi's member order where the provider holds it.
				data, held, err := ai.MarshalHookPayload(payload)
				if err == nil && !held {
					data, err = json.Marshal(payload)
				}
				if err != nil {
					return nil, err
				}
				if captured, err = orderedjson.Parse(data); err != nil {
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
		var fields []byte
		if tc.API == ai.APIAnthropicMessages {
			system, err := lastBlockMarker(captured, "system", false)
			if err != nil {
				return err
			}
			user, err := lastBlockMarker(captured, "messages", true)
			if err != nil {
				return err
			}
			fields = fmt.Appendf(nil, `{"system":%s,"user":%s}`, system, user)
		} else {
			fields = []byte{'{'}
			for i, key := range []string{"prompt_cache_key", "prompt_cache_retention", "prompt_cache_options"} {
				value, ok := captured.Get(key)
				if !ok {
					value = json.RawMessage("null")
				}
				if i > 0 {
					fields = append(fields, ',')
				}
				fields = fmt.Appendf(fields, "%q:%s", key, value)
			}
			fields = append(fields, '}')
		}
		if i > 0 {
			results.WriteByte(',')
		}
		fmt.Fprintf(&results, "%q:%s", tc.Name, fields)
	}
	results.WriteString("}\n")
	_, err = os.Stdout.Write(results.Bytes())
	return err
}

// lastBlockMarker is the cache_control of the first block of the list member name, or of the last block of the last message's content when messages is set,
// null when the marker or the block list is absent. Members keep the order the payload carried.
func lastBlockMarker(payload *orderedjson.Object, name string, messages bool) (json.RawMessage, error) {
	null := json.RawMessage("null")
	raw, _ := payload.Get(name)
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, fmt.Errorf("%s: no list", name)
	}
	item := items[0]
	if messages {
		item = items[len(items)-1]
		message, err := orderedjson.Parse(item)
		if err != nil {
			return nil, err
		}
		content, _ := message.Get("content")
		var blocks []json.RawMessage
		if json.Unmarshal(content, &blocks) != nil || len(blocks) == 0 {
			return null, nil
		}
		item = blocks[len(blocks)-1]
	}
	block, err := orderedjson.Parse(item)
	if err != nil {
		return nil, err
	}
	if marker, ok := block.Get("cache_control"); ok {
		return marker, nil
	}
	return null, nil
}
