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
				Login: func(interaction sdk.AuthInteraction, _ sdk.LoginOptions) (*sdk.Credential, error) {
					region, err := interaction.Prompt(map[string]any{"type": "select", "message": "Choose a region", "options": []map[string]any{{"id": "eu", "label": "Europe"}, {"id": "us", "label": "United States"}}})
					if err != nil {
						return nil, err
					}
					code, err := interaction.Prompt(map[string]any{"type": "text", "message": "Paste the sign-in code"})
					if err != nil {
						return nil, err
					}
					return sdk.NewCredential("type", "oauth", "access", code+"-"+region, "refresh", "refresh-"+region, "expires", 4102444800000), nil
				},
				Refresh: func(credential *sdk.Credential, _ context.Context) (*sdk.Credential, error) { return credential, nil },
				ToAuth: func(credential *sdk.Credential) (map[string]any, error) {
					return map[string]any{"apiKey": credential.String("access")}, nil
				},
			},
			APIKey: &sdk.APIKeyAuth{
				Name: name + " key",
				Resolve: func(input sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
					key := input.Credential.String("key")
					if key == "" {
						return nil, nil
					}
					return &sdk.AuthResult{Auth: map[string]any{"apiKey": key}}, nil
				},
				Login: func(interaction sdk.AuthInteraction) (*sdk.Credential, error) {
					baseURL, err := interaction.Prompt(map[string]any{"type": "text", "message": "Gateway URL"})
					if err != nil {
						return nil, err
					}
					key, err := interaction.Prompt(map[string]any{"type": "secret", "message": "Gateway key"})
					if err != nil {
						return nil, err
					}
					return sdk.NewCredential("env", sdk.NewOrderedObject("WIRING_GATEWAY_URL", baseURL), "key", key, "type", "api_key"), nil
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
