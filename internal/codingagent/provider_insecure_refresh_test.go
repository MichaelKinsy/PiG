// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestRegistrationWithRefreshModelsKeepsItsTLSOptIn (D36, wire:e8a6c64b): a registerProvider configuration with refreshModels is composed in the
// native collection (provider-composer.ts:613) and no longer sits in the dynamic map, but the transport still reads its Insecure flag through
// ProviderInsecure (coding/services.go builds the request provider from it). A re-registration merges like Pi's registerProvider (model-runtime.ts:921-933): an omitted (false) Insecure keeps the earlier opt-in (native_model_runtime.go, 68f87619b5), and a provider never opted in stays secure.
func TestRegistrationWithRefreshModelsKeepsItsTLSOptIn(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	registry.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{}))
	config := func(insecure bool) extension.ProviderConfig {
		return extension.ProviderConfig{
			BaseURL:  "https://gateway.example/v1",
			APIKey:   "key",
			API:      ai.APIOpenAICompletions,
			Insecure: insecure,
			Models:   []extension.ProviderModelConfig{{ID: "m", Name: "M", API: ai.APIOpenAICompletions}},
			RefreshModels: func(extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) {
				return nil, nil
			},
		}
	}
	if err := registry.RegisterExtensionProvider("insecure-refresh", config(true)); err != nil {
		t.Fatal(err)
	}
	if !registry.ProviderInsecure("insecure-refresh") {
		t.Fatal("a registration with refreshModels lost its Insecure flag")
	}
	if err := registry.RegisterExtensionProvider("insecure-refresh", config(false)); err != nil {
		t.Fatal(err)
	}
	if !registry.ProviderInsecure("insecure-refresh") {
		t.Fatal("a re-registration without Insecure dropped the earlier opt-in; Pi keeps an omitted field's previous value")
	}
	if err := registry.RegisterExtensionProvider("secure-refresh", config(false)); err != nil {
		t.Fatal(err)
	}
	if registry.ProviderInsecure("secure-refresh") {
		t.Fatal("a registration without Insecure reads as insecure")
	}
}
