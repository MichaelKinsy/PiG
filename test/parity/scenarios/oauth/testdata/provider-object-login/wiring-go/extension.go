// Package wiringlogin registers, through the Go SDK, the provider object that
// ../provider.mjs registers in TypeScript: an OAuth login with a loginLabel and
// a select prompt in its flow, and its own API-key login.
package wiringlogin

import (
	"context"
	"errors"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("wiring-go")
	name := "Wiring Go"
	label := name + " SSO"
	err := ext.RegisterNativeProvider(&sdk.Provider{
		ID:   "wiring-go",
		Name: name,
		Auth: sdk.ProviderAuth{
			OAuth: &sdk.OAuthAuth{
				Name:       name + " account",
				LoginLabel: &label,
				Login: func(interaction sdk.AuthInteraction) (map[string]any, error) {
					region, err := interaction.Prompt(map[string]any{"type": "select", "message": "Choose a region", "options": []map[string]any{{"id": "eu", "label": "Europe"}, {"id": "us", "label": "United States"}}})
					if err != nil {
						return nil, err
					}
					code, err := interaction.Prompt(map[string]any{"type": "text", "message": "Paste the sign-in code"})
					if err != nil {
						return nil, err
					}
					return map[string]any{"type": "oauth", "access": code + "-" + region, "refresh": "refresh-" + region, "expires": 4102444800000}, nil
				},
				Refresh: func(credential map[string]any, _ context.Context) (map[string]any, error) { return credential, nil },
				ToAuth:  func(credential map[string]any) (map[string]any, error) { return map[string]any{"apiKey": credential["access"]}, nil },
			},
			APIKey: &sdk.APIKeyAuth{
				Name: name + " key",
				Resolve: func(input sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
					key, _ := input.Credential["key"].(string)
					if key == "" {
						return nil, nil
					}
					return &sdk.AuthResult{Auth: map[string]any{"apiKey": key}}, nil
				},
				Login: func(interaction sdk.AuthInteraction) (map[string]any, error) {
					baseURL, err := interaction.Prompt(map[string]any{"type": "text", "message": "Gateway URL"})
					if err != nil {
						return nil, err
					}
					key, err := interaction.Prompt(map[string]any{"type": "secret", "message": "Gateway key"})
					if err != nil {
						return nil, err
					}
					return map[string]any{"type": "api_key", "key": key, "env": map[string]any{"WIRING_GATEWAY_URL": baseURL}}, nil
				},
			},
		},
		GetModels: func() ([]map[string]any, error) {
			return []map[string]any{{"id": "wiring-model", "name": "Wiring Model", "provider": "wiring-go", "api": "openai-completions", "baseUrl": "http://127.0.0.1:9", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}}, nil
		},
		Stream:       unused,
		StreamSimple: unused,
	})
	if err != nil {
		panic(err)
	}
	return ext
}

func unused(map[string]any, map[string]any, sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
	return nil, errors.New("unused")
}
