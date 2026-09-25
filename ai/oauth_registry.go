package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// builtInOAuthProviders mirrors upstream utils/oauth/index.ts provider registry,
// limited to the providers pig has actually ported in Go.
var builtInOAuthProviders = map[string]OAuthProviderInterface{
	"anthropic":      AnthropicOAuthProvider{},
	"github-copilot": copilotOAuthRegistryProvider{},
	"kimi-coding":    newKimiOAuthProvider(),
	"meta":           newMetaOAuthProvider(),
	"openai-codex":   CodexOAuthProvider{},
	"openrouter":     OpenRouterOAuthProvider{},
	"xai":            newXaiOAuthProvider(),
	"radius":         CreateRadiusOAuth(RadiusOAuthOptions{ID: "radius", Name: "Radius", Gateway: DefaultRadiusGateway}),
}

// copilotOAuthRegistryProvider adapts the GitHub Copilot device-flow helper to
// the shared OAuth provider registry used by model resolution and parity probes.
// Upstream includes github-copilot in the built-in OAuth provider list.
type copilotOAuthRegistryProvider struct{}

func (copilotOAuthRegistryProvider) ID() string               { return "github-copilot" }
func (copilotOAuthRegistryProvider) IsSubscription() bool     { return true }
func (copilotOAuthRegistryProvider) Name() string             { return "GitHub Copilot" }
func (copilotOAuthRegistryProvider) UsesCallbackServer() bool { return false }
func (copilotOAuthRegistryProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return copilotOAuthRegistryProvider{}.LoginContext(context.Background(), callbacks)
}
func (copilotOAuthRegistryProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	cred, err := LoginGitHubCopilot(ctx, CopilotLoginCallbacks{
		OnPrompt: func(ctx context.Context) (string, error) {
			if callbacks.OnPrompt == nil {
				return "", nil
			}
			return callbacks.OnPrompt(OAuthPrompt{Message: "GitHub Enterprise URL/domain", Placeholder: "company.ghe.com", AllowEmpty: true})
		},
		OnAuth: func(url, userCode string) {
			if callbacks.OnDeviceCode != nil {
				callbacks.OnDeviceCode(OAuthDeviceCodeInfo{VerificationURI: url, UserCode: userCode})
				return
			}
			if callbacks.OnAuth != nil {
				instructions := userCode
				if strings.TrimSpace(userCode) != "" {
					instructions = "Enter code: " + userCode
				}
				callbacks.OnAuth(OAuthAuthInfo{URL: url, Instructions: instructions})
			}
		},
		OnProgress: callbacks.OnProgress,
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	return OAuthCredentials{Refresh: cred.Refresh, Access: cred.Access, Expires: cred.Expires}, nil
}
func (copilotOAuthRegistryProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return copilotOAuthRegistryProvider{}.RefreshTokenContext(context.Background(), creds)
}
func (copilotOAuthRegistryProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	fresh, err := refreshCopilotToken(ctx, creds.Refresh, "")
	if err != nil {
		return OAuthCredentials{}, err
	}
	return OAuthCredentials{Refresh: fresh.Refresh, Access: fresh.Access, Expires: fresh.Expires}, nil
}
func (copilotOAuthRegistryProvider) GetAPIKey(creds OAuthCredentials) string { return creds.Access }

// customOAuthProviders holds dynamically registered OAuth providers.
// Mirrors upstream registerOAuthProvider (oauth/index.ts:64).
//
// customOAuthProvidersMu guards the map: unlike upstream's single-threaded JS,
// pig registers/unregisters providers from the extension-host lifecycle
// (goroutines on load/shutdown) while model resolution and the /login selector
// read concurrently.
var (
	customOAuthProviders   = map[string]OAuthProviderInterface{}
	customOAuthProvidersMu sync.RWMutex
)

// GetOAuthProvider returns an OAuth provider by id (custom first, then built-in).
func GetOAuthProvider(id string) (OAuthProviderInterface, bool) {
	customOAuthProvidersMu.RLock()
	p, ok := customOAuthProviders[id]
	customOAuthProvidersMu.RUnlock()
	if ok {
		return p, true
	}
	p, ok = builtInOAuthProviders[id]
	return p, ok
}

// GetOAuthProviders returns all OAuth providers (built-in + custom).
func GetOAuthProviders() []OAuthProviderInterface {
	customOAuthProvidersMu.RLock()
	defer customOAuthProvidersMu.RUnlock()
	providers := make([]OAuthProviderInterface, 0, len(builtInOAuthProviders)+len(customOAuthProviders))
	for _, p := range builtInOAuthProviders {
		providers = append(providers, p)
	}
	for _, p := range customOAuthProviders {
		providers = append(providers, p)
	}
	return providers
}

// RegisterOAuthProvider adds a custom OAuth provider. Mirrors upstream
// registerOAuthProvider (oauth/index.ts:64).
func RegisterOAuthProvider(id string, provider OAuthProviderInterface) {
	customOAuthProvidersMu.Lock()
	customOAuthProviders[id] = provider
	customOAuthProvidersMu.Unlock()
}

// UnregisterOAuthProvider removes a custom OAuth provider. Mirrors upstream
// unregisterOAuthProvider (oauth/index.ts:74).
func UnregisterOAuthProvider(id string) {
	customOAuthProvidersMu.Lock()
	delete(customOAuthProviders, id)
	customOAuthProvidersMu.Unlock()
}

// ResetOAuthProviders removes all custom providers, restoring built-ins only.
// Mirrors upstream resetOAuthProviders (oauth/index.ts:86).
func ResetOAuthProviders() {
	customOAuthProvidersMu.Lock()
	customOAuthProviders = map[string]OAuthProviderInterface{}
	customOAuthProvidersMu.Unlock()
}

type oauthContextRefresh interface {
	RefreshTokenContext(context.Context, OAuthCredentials) (OAuthCredentials, error)
}

// oauthContextAPIKey is implemented by providers whose key resolution can fail,
// such as an extension's getApiKey across a process boundary. Upstream lets
// that exception reach the model call.
type oauthContextAPIKey interface {
	GetAPIKeyContext(context.Context, OAuthCredentials) (string, error)
}

// GetOAuthAPIKey mirrors upstream getOAuthApiKey(): resolve an OAuth-backed API
// key, refreshing the credential if expired.
func GetOAuthAPIKey(providerID string, credentials map[string]OAuthCredentials) (*OAuthCredentials, string, error) {
	return GetOAuthAPIKeyContext(context.Background(), providerID, credentials)
}

// GetOAuthAPIKeyContext resolves an OAuth-backed API key with the owning operation's cancellation.
func GetOAuthAPIKeyContext(ctx context.Context, providerID string, credentials map[string]OAuthCredentials) (*OAuthCredentials, string, error) {
	provider, ok := GetOAuthProvider(providerID)
	if !ok {
		return nil, "", fmt.Errorf("unknown OAuth provider: %s", providerID)
	}

	creds, ok := credentials[providerID]
	if !ok {
		return nil, "", nil
	}

	if time.Now().UnixMilli() >= creds.Expires {
		var refreshed OAuthCredentials
		var err error
		if contextual, ok := provider.(oauthContextRefresh); ok {
			refreshed, err = contextual.RefreshTokenContext(ctx, creds)
		} else {
			refreshed, err = provider.RefreshToken(creds)
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to refresh OAuth token for %s: %w", providerID, err)
		}
		creds = refreshed
	}

	if contextual, ok := provider.(oauthContextAPIKey); ok {
		key, err := contextual.GetAPIKeyContext(ctx, creds)
		if err != nil {
			return nil, "", err
		}
		return &creds, key, nil
	}
	return &creds, provider.GetAPIKey(creds), nil
}

// ResolveOAuthAPIKeyFromStorage loads a provider credential from auth.json,
// refreshes it if needed, persists any refresh, and returns the provider's
// runtime API key string.
func ResolveOAuthAPIKeyFromStorage(storage *AuthStorage, providerID string) (string, error) {
	return ResolveOAuthAPIKeyFromStorageContext(context.Background(), storage, providerID)
}

// ResolveOAuthAPIKeyFromStorageContext loads, refreshes, and persists a provider credential with the owning operation's cancellation.
func ResolveOAuthAPIKeyFromStorageContext(ctx context.Context, storage *AuthStorage, providerID string) (string, error) {
	if storage == nil {
		return "", errors.New("nil auth storage")
	}
	credential, ok, err := storage.GetRaw(providerID)
	if err != nil || !ok || credential.Type != CredentialOAuth {
		return "", err
	}
	return resolveStoredOAuthAPIKey(ctx, storage, providerID, credential)
}

func resolveStoredOAuthAPIKey(ctx context.Context, storage *AuthStorage, providerID string, credential Credential) (string, error) {
	next, apiKey, err := GetOAuthAPIKeyContext(ctx, providerID, map[string]OAuthCredentials{
		providerID: {
			Refresh:   credential.Refresh,
			Access:    credential.Access,
			Expires:   credential.Expires,
			ProjectID: credential.ProjectID,
			Scope:     credential.Scope,
		},
	})
	if err != nil {
		return "", err
	}
	if next == nil || apiKey == "" {
		return "", nil
	}
	if next.Refresh != credential.Refresh || next.Access != credential.Access || next.Expires != credential.Expires || next.ProjectID != credential.ProjectID || next.Scope != credential.Scope {
		if err := storage.Set(providerID, Credential{
			Type:      CredentialOAuth,
			Refresh:   next.Refresh,
			Access:    next.Access,
			Expires:   next.Expires,
			ProjectID: next.ProjectID,
			Scope:     next.Scope,
		}); err != nil {
			return "", err
		}
	}
	return apiKey, nil
}

// ResolveStoredAPIKeyFromStorage returns the request key a stored auth.json
// credential supplies for providerID. Prefer ResolveStoredAPIKeyFromStorageContext
// when an operation context is available.
func ResolveStoredAPIKeyFromStorage(storage *AuthStorage, providerID string) (key string, ok bool, err error) {
	return ResolveStoredAPIKeyFromStorageContext(context.Background(), storage, providerID)
}

// ResolveStoredAPIKeyFromStorageContext returns a refreshed OAuth key or a
// resolved api_key credential. A stored credential is checked before caller
// fallbacks, and OAuth refresh follows ctx.
func ResolveStoredAPIKeyFromStorageContext(ctx context.Context, storage *AuthStorage, providerID string) (key string, ok bool, err error) {
	if storage == nil {
		return "", false, errors.New("nil auth storage")
	}
	credential, found, err := storage.GetRaw(providerID)
	if err != nil || !found {
		return "", false, err
	}
	switch credential.Type {
	case CredentialOAuth:
		key, err = resolveStoredOAuthAPIKey(ctx, storage, providerID, credential)
	case CredentialAPIKey:
		key = resolveStoredCredential(credential).Key
	}
	return key, key != "", err
}
