package codingagent_test

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// configureFauxAuth is Pi's test/suite/harness.ts withConfiguredAuth: it stores the faux credential and registers the faux provider with that
// key, so registration marks the provider configured in the availability snapshot (markProvisionallyConfigured) that prompt validation reads.
// The scripted model's own Provider still serves requests; the registered model, under an ID no scripted model uses, only carries the provider's configuration: a registered faux-1 would replace the scripted model at session start.
func configureFauxAuth(services *coding.AgentSessionServices, key string) error {
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: key}); err != nil {
		return err
	}
	return services.Registry().RegisterExtensionProvider("faux", extension.ProviderConfig{API: "openai-completions", APIKey: key, BaseURL: "https://faux.invalid", Models: []extension.ProviderModelConfig{
		{ID: "faux-auth", Name: "faux-auth", ContextWindow: 128_000, MaxTokens: 4096, Input: []string{"text"}},
	}})
}
