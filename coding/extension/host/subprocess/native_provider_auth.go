package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func (p *nativeProviderProxy) objectCall(ctx context.Context, method string, params any, callback nativeProviderCallback) (json.RawMessage, error) {
	return p.call(ctx, map[string]any{"method": method, "params": params}, nil, callback)
}

func nativeObjectValue[T any](p *nativeProviderProxy, ctx context.Context, method string, params any, callback nativeProviderCallback) (T, error) {
	var value T
	data, err := p.objectCall(ctx, method, params, callback)
	if err == nil {
		err = json.Unmarshal(data, &value)
	}
	return value, err
}

func authContextCallback(auth ai.AuthContext) nativeProviderCallback {
	return func(data json.RawMessage) (json.RawMessage, error) {
		var request struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"params"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, err
		}
		switch request.Method {
		case "env":
			value, present := auth.Env(request.Params.Name)
			if !present {
				return json.RawMessage("null"), nil
			}
			return json.Marshal(value)
		case "fileExists":
			return json.Marshal(auth.FileExists(request.Params.Path))
		default:
			return nil, fmt.Errorf("unexpected auth callback %s", request.Method)
		}
	}
}

func (p *nativeProviderProxy) auth(ctx context.Context) ai.ProviderAuth {
	var result ai.ProviderAuth
	if p.declaration.Auth == nil {
		return result
	}
	if p.declaration.Auth.APIKey != nil {
		result.APIKey = &ai.APIKeyAuth{Name: p.declaration.Auth.APIKey.Name,
			Resolve: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				return nativeObjectValue[*ai.AuthResult](p, ctx, "auth.apiKey.resolve", map[string]any{"credential": input.Credential}, authContextCallback(input.Ctx))
			},
		}
		if slices.Contains(p.declaration.Methods, "auth.apiKey.check") {
			result.APIKey.Check = func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
				return nativeObjectValue[*ai.AuthCheck](p, ctx, "auth.apiKey.check", map[string]any{"credential": input.Credential}, authContextCallback(input.Ctx))
			}
		}
		// An API-key method without a login is ambient-only (auth/types.ts ApiKeyAuth.login).
		if slices.Contains(p.declaration.Methods, "auth.apiKey.login") {
			result.APIKey.Login = p.apiKeyLogin
		}
	}
	if p.declaration.Auth.OAuth != nil {
		result.OAuth = &ai.OAuthAuth{Name: p.declaration.Auth.OAuth.Name, IsSubscription: p.declaration.Auth.OAuth.IsSubscription != nil && *p.declaration.Auth.OAuth.IsSubscription,
			Login: p.oauthLogin,
			Refresh: func(ctx context.Context, c ai.Credential) (ai.Credential, error) {
				return nativeObjectValue[ai.Credential](p, ctx, "auth.oauth.refresh", map[string]any{"credential": c}, nil)
			},
			ToAuth: func(c ai.Credential) (ai.ModelAuth, error) {
				return nativeObjectValue[ai.ModelAuth](p, ctx, "auth.oauth.toAuth", map[string]any{"credential": c}, nil)
			},
		}
		if label := p.declaration.Auth.OAuth.LoginLabel; label != nil {
			result.OAuth.LoginLabel = *label
		}
	}
	return result
}

// nativeCredentialSnapshot is the CredentialStore of one native provider's auth resolution: the credential the registry's transaction handed over. A refresh that ai.refreshStoredOAuthCredential started writes it from its own goroutine, which outlives a caller that gave up, so refreshing holds refresh for its whole run and settle waits for it.
type nativeCredentialSnapshot struct {
	providerID string

	// mu guards value and changed.
	mu      sync.Mutex
	value   *ai.Credential
	changed bool
	// refresh is held by Modify from before the refresh callback to after the write: a refresh that has started finishes, and is in value, before settle returns.
	refresh sync.Mutex
}

func (s *nativeCredentialSnapshot) current() *ai.Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *nativeCredentialSnapshot) Read(ctx context.Context, _ string) (*ai.Credential, error) {
	return s.current(), ctx.Err()
}
func (s *nativeCredentialSnapshot) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	value := s.current()
	if value == nil {
		return []ai.CredentialInfo{}, ctx.Err()
	}
	return []ai.CredentialInfo{{ProviderID: s.providerID, Type: value.Type}}, ctx.Err()
}
func (s *nativeCredentialSnapshot) Modify(_ context.Context, _ string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	s.refresh.Lock()
	defer s.refresh.Unlock()
	value, err := fn(s.current())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value != nil {
		s.value = value
		s.changed = true
	}
	return s.value, nil
}

// settle waits for a refresh that has started and returns the credential and whether a step changed it. A refresh that has not started when settle runs finds the cancelled caller's context in its callback and writes nothing.
func (s *nativeCredentialSnapshot) settle() (*ai.Credential, bool) {
	s.refresh.Lock()
	defer s.refresh.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.changed
}

func (s *nativeCredentialSnapshot) Delete(ctx context.Context, providerID string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if providerID == s.providerID && s.value != nil {
		s.value = nil
		s.changed = true
	}
	return nil
}

func (p *nativeProviderProxy) checkAuth(ctx context.Context, credential *ai.Credential) (*ai.AuthCheck, error) {
	return ai.CheckProviderAuth(ctx, p.declaration.ID, p.auth(ctx), &nativeCredentialSnapshot{value: credential, providerID: p.declaration.ID}, ai.DefaultProviderAuthContext())
}
func (p *nativeProviderProxy) resolveAuth(ctx context.Context, credential *ai.Credential, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
	store := &nativeCredentialSnapshot{value: credential, providerID: p.declaration.ID}
	result, err := ai.ResolveProviderAuth(ctx, p.declaration.ID, p.auth(ctx), store, ai.DefaultProviderAuthContext(), overrides)
	// pig divergence (D237): ResolveProviderAuth returns when ctx ends, but an OAuth refresh it started goes on without ctx (resolve.ts:refreshStoredOAuthCredential, AbortSignal.timeout(15 s)) and may already have spent the refresh token. Wait for it, and return the rotated credential even when a later step failed with the abort, so the registry's transaction saves it.
	value, changed := store.settle()
	if changed {
		return result, value, err
	}
	return result, nil, err
}
func (p *nativeProviderProxy) refreshCredential(ctx context.Context, credential *ai.Credential) (*ai.Credential, *ai.Credential, error) {
	auth := p.auth(ctx)
	if credential != nil && credential.Type == ai.CredentialOAuth {
		if auth.OAuth == nil {
			return nil, nil, nil
		}
		if float64(time.Now().UnixMilli()) < credential.ExpiresMillis() {
			return credential, nil, nil
		}
		// pig divergence (D237): the request may already have rotated the single-use refresh token, so exit's cancellation must not cut it (as ai.Models' refresh, models.ts:resolveRefreshCredential).
		value, err := ai.RefreshOAuthDetached(ctx, auth.OAuth, *credential)
		if err != nil {
			return nil, nil, err
		}
		return &value, &value, nil
	}
	if auth.APIKey == nil {
		return nil, nil, nil
	}
	if credential != nil && credential.Type != ai.CredentialAPIKey {
		credential = nil
	}
	result, err := auth.APIKey.Resolve(ctx, ai.APIKeyAuthInput{Ctx: ai.DefaultProviderAuthContext(), Credential: credential})
	if err != nil || result == nil {
		return nil, nil, err
	}
	return &ai.Credential{Type: ai.CredentialAPIKey, Key: result.Auth.APIKey, Env: result.Env}, nil, nil
}
