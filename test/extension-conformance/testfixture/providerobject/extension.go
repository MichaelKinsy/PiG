package providerobject

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("provider-object-go")
	if os.Getenv("CARRIER_ROLE") != "reader" {
		if err := ext.RegisterNativeProvider(makeProvider()); err != nil {
			panic(err)
		}
	}
	ext.Command("remote-carrier-probe", "Exercise a foreign Provider", probe)
	return ext
}
func makeProvider() *sdk.Provider {
	var mu sync.Mutex
	model := map[string]any{"id": "carrier-model", "name": "Carrier", "provider": "carrier-provider", "api": "openai-completions", "baseUrl": "http://127.0.0.1:9", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}
	models := []map[string]any{model}
	stream := func(m, _ map[string]any, options sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
		meta, _ := options.Values["metadata"].(map[string]any)
		if meta["fail"] == true {
			return nil, errors.New("carrier stream failed")
		}
		// Pi types.ts:1917-1920: a provider's streamSimple invokes options.onPayload before the request and options.onResponse after the response.
		// The values are distinctive so a caller that never reaches the host's hooks cannot satisfy the conformance row by default.
		if options.OnPayload != nil {
			if _, err := options.OnPayload(map[string]any{"marker": "carrier-payload"}, m); err != nil {
				return nil, err
			}
		}
		if options.OnResponse != nil {
			if err := options.OnResponse(map[string]any{"status": 207, "headers": map[string]any{"x-carrier": "carrier-response"}}, m); err != nil {
				return nil, err
			}
		}
		s := sdk.CreateAssistantMessageEventStream()
		message := map[string]any{"role": "assistant", "api": m["api"], "provider": m["provider"], "model": m["id"], "content": []any{}, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "stop", "timestamp": 1}
		if meta["wait"] == true {
			context.AfterFunc(options.Signal, func() {
				message["stopReason"] = "aborted"
				message["errorMessage"] = "carrier cancelled"
				s.Push(map[string]any{"type": "error", "reason": "aborted", "error": message})
			})
		} else {
			text := "carrier answer"
			if method, ok := meta["method"].(string); ok {
				text = method
			}
			message["content"] = []any{map[string]any{"type": "text", "text": text}}
			s.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
		}
		return s, nil
	}
	return &sdk.Provider{ID: "carrier-provider", Name: "Carrier Provider", BaseURL: new(model["baseUrl"].(string)), Headers: map[string]string{"X-Carrier": "present"},
		Auth: sdk.ProviderAuth{
			APIKey: &sdk.APIKeyAuth{Name: "Carrier API key",
				Check: func(input sdk.APIKeyAuthInput) (*sdk.AuthCheck, error) {
					source, err := input.Ctx.Env("CARRIER_SOURCE")
					return &sdk.AuthCheck{Type: "api_key", Source: source}, err
				},
				Resolve: func(input sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
					key, err := input.Ctx.Env("CARRIER_KEY")
					auth := map[string]any{}
					if key != nil {
						auth["apiKey"] = *key
					}
					if value, ok := input.Credential["key"]; ok {
						auth["apiKey"] = value
					}
					return &sdk.AuthResult{Auth: auth, Source: new("caller context")}, err
				},
				Login: func(input sdk.AuthInteraction) (map[string]any, error) {
					key, err := input.Prompt(map[string]any{"type": "secret", "message": "Carrier key"})
					return map[string]any{"type": "api_key", "key": key}, err
				},
			},
			OAuth: &sdk.OAuthAuth{Name: "Carrier OAuth", IsSubscription: new(true), LoginLabel: new("Carrier login"),
				Login: func(input sdk.AuthInteraction) (map[string]any, error) {
					key, err := input.Prompt(map[string]any{"type": "text", "message": "Carrier OAuth"})
					return map[string]any{"type": "oauth", "access": key, "refresh": "refresh", "expires": 100}, err
				},
				Refresh: func(input map[string]any, _ context.Context) (map[string]any, error) {
					result := map[string]any{}
					maps.Copy(result, input)
					result["access"] = "rotated"
					result["expires"] = 200
					return result, nil
				},
				ToAuth: func(input map[string]any) (map[string]any, error) {
					return map[string]any{"apiKey": input["access"], "baseUrl": "https://carrier.invalid"}, nil
				},
			},
		},
		GetModels: func() ([]map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			return append([]map[string]any{}, models...), nil
		},
		// GetAllModels lists a model of the all-types catalog that getModels (chat) does not, and FilterAllModels shows every model to the "all" credential.
		GetAllModels: func() ([]map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			extra := map[string]any{}
			maps.Copy(extra, model)
			extra["id"] = "carrier-all-only"
			return append(append([]map[string]any{}, models...), extra), nil
		},
		FilterAllModels: func(models []map[string]any, credential map[string]any) ([]map[string]any, error) {
			if credential["key"] == "all" {
				return models, nil
			}
			return []map[string]any{}, nil
		},
		FilterModels: func(models []map[string]any, credential map[string]any) ([]map[string]any, error) {
			if credential["key"] == "selected" {
				return models[:1], nil
			}
			return []map[string]any{}, nil
		},
		RefreshModels: func(input sdk.RefreshModelsContext) error {
			if input.Force == nil || !*input.Force {
				return nil
			}
			_, err := input.Publish(sdk.ModelsPublication{Persist: json.RawMessage("null"), Update: func() error {
				mu.Lock()
				defer mu.Unlock()
				extra := map[string]any{}
				maps.Copy(extra, model)
				extra["id"] = "refreshed"
				models = append(models, extra)
				return nil
			}})
			return err
		},
		Stream: stream,
		StreamSimple: func(m, c map[string]any, o sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
			o.Values = map[string]any{"metadata": map[string]any{"method": "simple"}}
			return stream(m, c, o)
		},
		FetchDeferred: func(m, h map[string]any, o sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
			o.Values = map[string]any{"metadata": map[string]any{"method": h["id"]}}
			return stream(m, nil, o)
		},
		CancelDeferred: func(_ map[string]any, h map[string]any, _ sdk.ProviderStreamOptions) error {
			if h["id"] != "deferred" {
				return errors.New("wrong deferred handle")
			}
			return nil
		},
	}
}
func probe(ctx sdk.Context, path string) error {
	p, err := ctx.ModelRegistry().GetRegisteredNativeProvider("carrier-provider")
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("foreign Provider is absent")
	}
	models, err := p.GetModels()
	if err != nil {
		return err
	}
	model := models[0]
	filtered, err := p.FilterModels(models, map[string]any{"type": "api_key", "key": "selected"})
	if err != nil {
		return err
	}
	if len(filtered) != 1 {
		return errors.New("filter did not select a model")
	}
	input := sdk.APIKeyAuthInput{Signal: context.Background(), Ctx: sdk.AuthContext{Env: func(name string) (*string, error) { return new("injected:" + name), nil }, FileExists: func(string) (bool, error) { return false, nil }}}
	check, err := p.Auth.APIKey.Check(input)
	if err != nil {
		return err
	}
	auth, err := p.Auth.APIKey.Resolve(input)
	if err != nil {
		return err
	}
	interaction := sdk.AuthInteraction{Signal: context.Background(), Prompt: func(prompt map[string]any) (string, error) { return prompt["message"].(string), nil }, Notify: func(map[string]any) error { return nil }}
	apiLogin, err := p.Auth.APIKey.Login(interaction)
	if err != nil {
		return err
	}
	login, err := p.Auth.OAuth.Login(interaction)
	if err != nil {
		return err
	}
	rotated, err := p.Auth.OAuth.Refresh(login, context.Background())
	if err != nil {
		return err
	}
	oauth, err := p.Auth.OAuth.ToAuth(rotated)
	if err != nil {
		return err
	}
	order := []string{}
	err = p.RefreshModels(sdk.RefreshModelsContext{Force: new(true), Signal: context.Background(), Publish: func(publication sdk.ModelsPublication) (bool, error) {
		if string(publication.Persist) != "null" {
			return false, errors.New("persist presence lost")
		}
		order = append(order, "persist")
		if err := publication.Update(); err != nil {
			return false, err
		}
		order = append(order, "update")
		return true, nil
	}})
	if err != nil {
		return err
	}
	stream, err := p.Stream(model, map[string]any{"messages": []any{}}, sdk.ProviderStreamOptions{})
	if err != nil {
		return err
	}
	result := stream.Result()
	simple, err := p.StreamSimple(model, map[string]any{"messages": []any{}}, sdk.ProviderStreamOptions{})
	if err != nil {
		return err
	}
	deferred, err := p.FetchDeferred(model, map[string]any{"id": "deferred"}, sdk.ProviderStreamOptions{})
	if err != nil {
		return err
	}
	if err := p.CancelDeferred(model, map[string]any{"id": "deferred"}, sdk.ProviderStreamOptions{}); err != nil {
		return err
	}
	signal, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting, err := p.Stream(model, map[string]any{"messages": []any{}}, sdk.ProviderStreamOptions{Signal: signal, Values: map[string]any{"metadata": map[string]any{"wait": true}}})
	if err != nil {
		return err
	}
	cancel()
	cancelled := waiting.Result()
	models, err = p.GetModels()
	if err != nil {
		return err
	}
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model["id"].(string))
	}
	data, err := json.Marshal(map[string]any{"headers": p.Headers, "check": check, "auth": auth, "apiLogin": apiLogin, "login": login, "rotated": rotated, "oauth": oauth, "order": order, "models": ids, "result": result["content"], "simple": simple.Result()["content"], "deferred": deferred.Result()["content"], "cancelled": cancelled["stopReason"]})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
