package codingagent

import (
	"slices"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/tui"
)

// loginNameCollator orders the login list the way upstream sorts provider options by name with localeCompare.
var loginNameCollator = collate.New(language.Und)

// withNativeAPIKeyProviders adds the providers an extension registered with an API-key auth method (a registered native provider such
// as the built-in llama.cpp) to the API-key login list at their name-ordered positions, as Pi lists every provider its model runtime
// composes with that method.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:getLoginProviderOptions
func (m *InteractiveMode) withNativeAPIKeyProviders(providers []tui.OAuthProvider) []tui.OAuthProvider {
	registry := m.opts.ModelRegistry
	if registry == nil {
		return providers
	}
	for _, id := range registry.GetRegisteredProviderIDs() {
		provider := registry.GetProvider(id)
		if provider == nil || provider.Auth.APIKey == nil || slices.ContainsFunc(providers, func(listed tui.OAuthProvider) bool { return listed.ID == id }) {
			continue
		}
		name := provider.Name
		if name == "" {
			name = id
		}
		index := slices.IndexFunc(providers, func(candidate tui.OAuthProvider) bool {
			return loginNameCollator.CompareString(candidate.Name, name) > 0
		})
		if index < 0 {
			index = len(providers)
		}
		providers = slices.Insert(providers, index, tui.OAuthProvider{ID: id, Name: name, AuthType: "api_key"})
	}
	return providers
}
