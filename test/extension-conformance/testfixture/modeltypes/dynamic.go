package modeltypes

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// dynamicConfig is the provider config the dynamic_provider tool registers: an OAuth config whose usesCallbackServer is set and whose modifyModels projects the models by the stored credential (types.ts:2026, 2034), and a refreshModels that reads its context and publishes (types.ts:1930). Every value the host asserts is one these callbacks produce, so the Rust, Python, Lisp and Node fixtures must compute the same.
func dynamicConfig() sdk.ProviderConfig {
	return sdk.ProviderConfig{
		"api": "dyn-api", "baseUrl": "https://dyn.test/v1", "apiKey": "dyn-key",
		"models": []any{map[string]any{"id": "a", "name": "A", "reasoning": false, "input": []string{"text"}, "cost": zeroCost, "contextWindow": 1000, "maxTokens": 100}},
		"oauth": &sdk.OAuthProvider{
			Name:               "Dynamic OAuth",
			UsesCallbackServer: true,
			Login:              func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) { return sdk.OAuthCredentials{Access: "login"}, nil },
			RefreshToken:       func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) { return creds, nil },
			GetAPIKey:          func(creds sdk.OAuthCredentials) string { return creds.Access },
			// The models without the "drop" id, in reverse order, each named after the credential's access token.
			ModifyModels: func(models []map[string]any, creds sdk.OAuthCredentials) ([]map[string]any, error) {
				if creds.Access == "boom" {
					return nil, errors.New("modify boom")
				}
				out := []map[string]any{}
				for _, model := range slices.Backward(models) {
					if model["id"] == "drop" {
						continue
					}
					modified := maps.Clone(model)
					modified["name"] = fmt.Sprintf("%v+%s", model["name"], creds.Access)
					out = append(out, modified)
				}
				return out, nil
			},
		},
		"refreshModels": sdk.ProviderRefreshModelsFunc(func(refresh sdk.RefreshModelsContext) ([]map[string]any, error) {
			if refresh.Credential.String("access") == "boom" {
				return nil, errors.New("refresh boom")
			}
			// A persisted catalog first, then a publication whose update runs here while the host applies it.
			if _, err := refresh.Publish(sdk.ModelsPublication{Persist: []byte(`{"models":[],"checkedAt":99}`)}); err != nil {
				return nil, err
			}
			updated := false
			accepted, err := refresh.Publish(sdk.ModelsPublication{Update: func() error { updated = true; return nil }})
			if err != nil {
				return nil, err
			}
			or := func(value any, none string) string {
				if value == nil {
					return none
				}
				return fmt.Sprint(value)
			}
			var force, credential, stored, base any
			if refresh.Force != nil {
				force = *refresh.Force
			}
			if refresh.Credential != nil {
				credential = refresh.Credential.String("access")
			}
			if refresh.Stored != nil {
				stored = refresh.Stored["checkedAt"]
			}
			if refresh.BaseURL != "" {
				base = refresh.BaseURL
			}
			name := fmt.Sprintf("refresh|net=%t|force=%s|cred=%s|stored=%s|base=%s|updated=%t|accepted=%t", refresh.AllowNetwork, or(force, "none"), or(credential, "none"), or(stored, "none"), or(base, "none"), updated, accepted)
			return []map[string]any{{"id": "r1", "name": name, "api": "dyn-api", "reasoning": false, "input": []string{"text"}, "cost": zeroCost, "contextWindow": 1000, "maxTokens": 100}}, nil
		}),
	}
}

// registerDynamicProvider registers the provider while the factory runs.
func registerDynamicProvider(e *sdk.Extension) {
	e.RegisterProvider("dyn", dynamicConfig())
}
