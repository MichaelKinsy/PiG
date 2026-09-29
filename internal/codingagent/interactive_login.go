package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) providerAuth(id string) ai.ProviderAuth {
	if m.opts.Llama != nil && m.opts.Llama.Provider().ID == id {
		return ai.ProviderAuth{APIKey: llamaAPIKeyAuth(m.opts.Llama.Provider())}
	}
	if registry := m.opts.ModelRegistry; registry != nil {
		if provider := registry.GetProvider(id); provider != nil {
			return provider.Auth
		}
		auth, _, _ := registry.registryAuthConfig(id)
		return auth
	}
	auth, _ := ai.BuiltinProviderAuth(id)
	return auth
}

func (m *InteractiveMode) getLogoutProviderOptions() ([]tui.OAuthProvider, error) {
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return nil, err
	}
	credentials, err := auth.Load()
	if err != nil {
		return nil, err
	}
	var providers []tui.OAuthProvider
	for id, credential := range credentials {
		name := buildAuthProviderName(id)
		if registry := m.opts.ModelRegistry; registry != nil {
			if provider := registry.GetProvider(id); provider != nil {
				name = provider.Name
			}
		}
		providers = append(providers, tui.OAuthProvider{ID: id, Name: name, AuthType: string(credential.Type), Stored: true, StoredType: string(credential.Type), AuthStatusSource: "stored credential"})
	}
	for _, provider := range m.oauthProviders() {
		if slices.ContainsFunc(providers, func(p tui.OAuthProvider) bool { return p.ID == provider.ID() }) {
			continue
		}
		if store, ok := provider.(ai.OAuthCredentialStore); ok {
			if status, ok := store.OAuthCredentialStatus(); ok {
				providers = append(providers, tui.OAuthProvider{ID: provider.ID(), Name: provider.Name(), AuthType: status.AuthType, Stored: true, StoredType: status.AuthType, AuthStatusSource: "stored credential"})
			}
		}
	}
	sortAuthProviders(providers)
	return providers, nil
}

func sortAuthProviders(providers []tui.OAuthProvider) {
	collator := collate.New(language.Und)
	slices.SortStableFunc(providers, func(a, b tui.OAuthProvider) int { return collator.CompareString(a.Name, b.Name) })
}

func (m *InteractiveMode) getLoginProviderOptions(includeStatus ...bool) []tui.OAuthProvider {
	providers := append(m.oauthProviderList("login-oauth", includeStatus...), m.oauthProviderList("login-api-key", includeStatus...)...)
	if registry := m.opts.ModelRegistry; registry != nil {
		ids := registry.GetRegisteredProviderIDs()
		registry.mu.RLock()
		if registry.config != nil {
			for id := range registry.config.Providers {
				if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
		}
		registry.mu.RUnlock()
		for _, id := range ids {
			auth := m.providerAuth(id)
			name := registry.GetProviderDisplayName(id)
			for _, kind := range []string{"oauth", "api_key"} {
				index := slices.IndexFunc(providers, func(p tui.OAuthProvider) bool { return p.ID == id && p.AuthType == kind })
				option := tui.OAuthProvider{ID: id, Name: name, AuthType: kind}
				if index >= 0 {
					option = providers[index]
				}
				option.Name = name
				switch {
				case kind == "oauth" && auth.OAuth != nil:
					option.MethodName = auth.OAuth.Name
					option.LoginLabel = auth.OAuth.LoginLabel
				case kind == "api_key" && auth.APIKey != nil:
					option.MethodName = auth.APIKey.Name
				default:
					if index >= 0 {
						providers = slices.Delete(providers, index, index+1)
					}
					continue
				}
				if len(includeStatus) == 0 || includeStatus[0] {
					status := registry.GetProviderAuthStatus(id)
					if status.Configured {
						option.AuthStatusSource = string(status.Source)
						option.AuthStatusLabel = status.Label
						if status.Source == ai.AuthSourceStored {
							store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
							if err == nil {
								if credential, exists, err := store.GetRaw(id); err == nil && exists {
									option.Stored = true
									option.StoredType = string(credential.Type)
								}
							}
						}
					}
				}
				if index >= 0 {
					providers[index] = option
				} else {
					providers = append(providers, option)
				}
			}
		}
	}
	sortAuthProviders(providers)
	return providers
}

type loginProviderCompletionOption struct {
	id, name  string
	authTypes []string
}

func (m *InteractiveMode) loginArgCompletions(prefix string) []tui.AutocompleteItem {
	var providers []loginProviderCompletionOption
	for _, option := range m.getLoginProviderOptions(false) {
		index := slices.IndexFunc(providers, func(p loginProviderCompletionOption) bool { return p.id == option.ID })
		if index < 0 {
			providers = append(providers, loginProviderCompletionOption{id: option.ID, name: option.Name, authTypes: []string{option.AuthType}})
			continue
		}
		if !slices.Contains(providers[index].authTypes, option.AuthType) {
			providers[index].authTypes = append(providers[index].authTypes, option.AuthType)
			slices.SortFunc(providers[index].authTypes, func(a, b string) int {
				if a == b {
					return 0
				}
				if a == "oauth" {
					return -1
				}
				return 1
			})
		}
	}
	filtered := tui.FuzzyFilter(providers, prefix, func(p loginProviderCompletionOption) string {
		authTypes := make([]string, len(p.authTypes))
		for i, kind := range p.authTypes {
			authTypes[i] = kind + " " + tui.FormatAuthSelectorProviderType(kind)
		}
		return p.id + " " + p.name + " " + strings.Join(authTypes, " ")
	})
	var out []tui.AutocompleteItem
	for _, p := range filtered {
		kinds := make([]string, len(p.authTypes))
		for i, kind := range p.authTypes {
			kinds[i] = tui.FormatAuthSelectorProviderType(kind)
		}
		description := strings.Join(kinds, "/")
		if p.name != p.id {
			description = p.name + " · " + description
		}
		out = append(out, tui.AutocompleteItem{Value: p.id, Label: p.id, Description: description})
	}
	return out
}

func (m *InteractiveMode) showLoginAuthTypeSelector(providers []tui.OAuthProvider) (string, bool) {
	title, oauthLabel := "Select authentication method:", "Sign in with an account"
	if len(providers) > 0 {
		title = "Select authentication method for " + providers[0].Name + ":"
		for _, provider := range providers {
			if provider.AuthType == "oauth" && provider.LoginLabel != "" {
				oauthLabel = provider.LoginLabel
			}
		}
	}
	index, ok := m.runEditorSlotExtensionSelector(tui.NewExtensionSelector(title, []string{oauthLabel, "Sign in with an API key"}))
	if index == 1 {
		return "api_key", ok
	}
	return "oauth", ok
}

// loginContext retains the owning interactive lifetime for all login work.
func (m *InteractiveMode) loginContext() context.Context {
	if m.runCtx != nil {
		return m.runCtx
	}
	return context.Background()
}
