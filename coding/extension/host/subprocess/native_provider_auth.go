package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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

type nativeCredentialSnapshot struct {
	value      *ai.Credential
	providerID string
	changed    bool
}

func (s *nativeCredentialSnapshot) Read(ctx context.Context, _ string) (*ai.Credential, error) {
	return s.value, ctx.Err()
}
func (s *nativeCredentialSnapshot) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	if s.value == nil {
		return []ai.CredentialInfo{}, ctx.Err()
	}
	return []ai.CredentialInfo{{ProviderID: s.providerID, Type: s.value.Type}}, ctx.Err()
}
func (s *nativeCredentialSnapshot) Modify(_ context.Context, _ string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	value, err := fn(s.value)
	if err != nil {
		return nil, err
	}
	if value != nil {
		s.value = value
		s.changed = true
	}
	return s.value, nil
}

func (s *nativeCredentialSnapshot) Delete(ctx context.Context, providerID string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
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
	if store.changed {
		return result, store.value, err
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
		value, err := auth.OAuth.Refresh(ctx, *credential)
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
