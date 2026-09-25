package ai

// Request-auth resolution. Mirrors upstream packages/ai/src/auth/types.ts
// (ModelAuth, AuthResult, AuthCheck, AuthContext, ApiKeyAuth, OAuthAuth,
// ProviderAuth), packages/ai/src/auth/resolve.ts (resolveProviderAuth,
// ModelsError), packages/ai/src/auth/context.ts (defaultProviderAuthContext)
// and the checkAuth/getAuth paths of packages/ai/src/models.ts.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"strings"
	"time"
)

// ModelAuth is request auth for a single model request. Mirrors upstream
// ModelAuth.
type ModelAuth struct {
	APIKey  string
	Headers ProviderHeaders
	BaseURL string
}

// AuthResult is the result of resolving auth for a provider or model.
type AuthResult struct {
	Auth ModelAuth
	// Env holds provider-scoped environment/config values resolved from the
	// credential and ambient context.
	Env map[string]string
	// Source is a status label such as "OPENAI_API_KEY" or "OAuth".
	Source string
}

// AuthCheck reports that auth is configured without resolving it.
type AuthCheck struct {
	Source string
	Type   CredentialType
}

// AuthContext is environment access for auth resolution. Env reports a
// value only when it is set and not blank.
type AuthContext struct {
	Env        func(name string) (string, bool)
	FileExists func(path string) bool
}

// DefaultProviderAuthContext reads process environment variables and checks
// files on disk. Mirrors upstream defaultProviderAuthContext.
func DefaultProviderAuthContext() AuthContext {
	return AuthContext{
		Env: func(name string) (string, bool) {
			value, ok := os.LookupEnv(name)
			if !ok || strings.TrimSpace(value) == "" {
				return "", false
			}
			return value, true
		},
		FileExists: func(path string) bool {
			if strings.HasPrefix(path, "~") {
				home, err := os.UserHomeDir()
				if err != nil {
					return false
				}
				path = home + path[1:]
			}
			_, err := os.Stat(path)
			return err == nil
		},
	}
}

// APIKeyAuthInput is the input to APIKeyAuth.Check and APIKeyAuth.Resolve.
// Credential is the stored api_key credential, if any.
type APIKeyAuthInput struct {
	Ctx        AuthContext
	Credential *Credential
}

// APIKeyAuth resolves stored-key and ambient API-key auth. Check is an
// optional side-effect-free availability check; without it availability is
// checked by resolving. Resolve returns nil when auth is not configured.
type APIKeyAuth struct {
	Name    string
	Check   func(ctx context.Context, input APIKeyAuthInput) (*AuthCheck, error)
	Resolve func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error)
}

// OAuthAuth refreshes stored OAuth credentials and derives request auth
// from them.
type OAuthAuth struct {
	Name           string
	IsSubscription bool
	Refresh        func(ctx context.Context, credential Credential) (Credential, error)
	ToAuth         func(credential Credential) (ModelAuth, error)
}

// ProviderAuth lists the auth methods a provider supports.
type ProviderAuth struct {
	APIKey *APIKeyAuth
	OAuth  *OAuthAuth
}

// AuthResolutionOverrides mirrors upstream AuthResolutionOverrides.
type AuthResolutionOverrides struct {
	APIKey *string
	Env    map[string]string
	// MinOAuthValidityMs requires this much remaining OAuth-token validity;
	// nil keeps the default five-minute refresh window without enforcing it.
	MinOAuthValidityMs *float64
}

// ModelsErrorCode mirrors upstream ModelsErrorCode.
type ModelsErrorCode string

const (
	ModelsErrorModelSource     ModelsErrorCode = "model_source"
	ModelsErrorModelValidation ModelsErrorCode = "model_validation"
	ModelsErrorProvider        ModelsErrorCode = "provider"
	ModelsErrorStream          ModelsErrorCode = "stream"
	ModelsErrorAuth            ModelsErrorCode = "auth"
	ModelsErrorOAuth           ModelsErrorCode = "oauth"
)

// ModelsError is a coded model-collection failure. Its message carries the
// cause's detail because callers surface only the message.
type ModelsError struct {
	Code    ModelsErrorCode
	Message string
	Cause   error
}

// NewModelsError mirrors the upstream ModelsError constructor.
func NewModelsError(code ModelsErrorCode, message string, cause error) *ModelsError {
	if cause != nil {
		if detail := strings.TrimSpace(cause.Error()); detail != "" && !strings.Contains(message, detail) {
			message = message + ": " + detail
		}
	}
	return &ModelsError{Code: code, Message: message, Cause: cause}
}

func (e *ModelsError) Error() string { return e.Message }

func (e *ModelsError) Unwrap() error { return e.Cause }

const (
	defaultOAuthMinimumValidityMs = float64(5 * 60 * 1000)
	defaultOAuthRefreshTimeout    = 15 * time.Second
)

// ResolveProviderAuth resolves request auth for a provider. A stored
// credential owns the provider: ambient environment is consulted only when
// nothing is stored, with no fallback after a failed refresh or for a
// credential type without a matching handler. Mirrors upstream
// resolveProviderAuth.
func ResolveProviderAuth(ctx context.Context, providerID string, auth ProviderAuth, credentials CredentialStore, authContext AuthContext, overrides AuthResolutionOverrides) (*AuthResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requestContext := authContext
	if overrides.Env != nil {
		requestContext = overlayEnvAuthContext(authContext, overrides.Env)
	}
	if overrides.APIKey != nil && auth.APIKey != nil {
		return resolveAPIKey(ctx, requestContext, auth.APIKey, providerID, &Credential{Type: CredentialAPIKey, Key: *overrides.APIKey, Env: overrides.Env})
	}
	stored, err := readProviderCredential(ctx, credentials, providerID)
	if err != nil {
		return nil, err
	}
	if stored != nil {
		if stored.Type == CredentialOAuth && auth.OAuth != nil {
			return resolveStoredOAuth(ctx, credentials, providerID, auth.OAuth, *stored, overrides.MinOAuthValidityMs)
		}
		if stored.Type == CredentialAPIKey && auth.APIKey != nil {
			credential := stored
			if overrides.Env != nil {
				merged := cloneCredential(*stored)
				if merged.Env == nil {
					merged.Env = map[string]string{}
				}
				maps.Copy(merged.Env, overrides.Env)
				credential = &merged
			}
			return resolveAPIKey(ctx, requestContext, auth.APIKey, providerID, credential)
		}
		return nil, nil
	}
	if auth.APIKey == nil {
		return nil, nil
	}
	return resolveAPIKey(ctx, requestContext, auth.APIKey, providerID, nil)
}

// CheckProviderAuth reports whether a provider's auth is configured without
// refreshing OAuth or running request-time work where a check exists.
// Mirrors upstream Models.checkAuth for a known provider.
func CheckProviderAuth(ctx context.Context, providerID string, auth ProviderAuth, credentials CredentialStore, authContext AuthContext) (*AuthCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credential, err := readProviderCredential(ctx, credentials, providerID)
	if err != nil {
		return nil, err
	}
	if credential != nil && credential.Type == CredentialOAuth {
		if auth.OAuth == nil {
			return nil, nil
		}
		return &AuthCheck{Source: "OAuth", Type: CredentialOAuth}, nil
	}
	if auth.APIKey == nil {
		return nil, nil
	}
	if auth.APIKey.Check != nil {
		input := APIKeyAuthInput{Ctx: authContext}
		if credential != nil && credential.Type == CredentialAPIKey {
			input.Credential = credential
		}
		check, err := auth.APIKey.Check(ctx, input)
		if err != nil {
			return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("API key auth check failed for provider %s", providerID), err)
		}
		return check, nil
	}
	resolution, err := ResolveProviderAuth(ctx, providerID, auth, credentials, authContext, AuthResolutionOverrides{})
	if err != nil || resolution == nil {
		return nil, err
	}
	return &AuthCheck{Source: resolution.Source, Type: CredentialAPIKey}, nil
}

func overlayEnvAuthContext(base AuthContext, env map[string]string) AuthContext {
	return AuthContext{
		Env: func(name string) (string, bool) {
			if value := env[name]; value != "" {
				return value, true
			}
			return base.Env(name)
		},
		FileExists: base.FileExists,
	}
}

// resolveStoredOAuth refreshes with double-checked locking: a token with less
// than the minimum validity remaining locks, re-checks expiry under the lock,
// refreshes once, and persists the rotated credential before release.
func resolveStoredOAuth(ctx context.Context, credentials CredentialStore, providerID string, oauth *OAuthAuth, stored Credential, minOAuthValidityMs *float64) (*AuthResult, error) {
	minimumValidityMs := defaultOAuthMinimumValidityMs
	if minOAuthValidityMs != nil {
		minimumValidityMs = math.Max(minimumValidityMs, *minOAuthValidityMs)
	}
	expiresSoon := func(credential Credential) bool {
		return float64(time.Now().UnixMilli())+minimumValidityMs >= float64(credential.Expires)
	}
	credential := stored
	if expiresSoon(credential) {
		post, err := credentials.Modify(ctx, providerID, func(current *Credential) (*Credential, error) {
			if current == nil || current.Type != CredentialOAuth || !expiresSoon(*current) {
				return nil, nil
			}
			refreshed, err := refreshOAuthWithTimeout(ctx, oauth, *current)
			if err != nil {
				return nil, NewModelsError(ModelsErrorOAuth, fmt.Sprintf("OAuth refresh failed for %s", providerID), err)
			}
			return &refreshed, nil
		})
		if err != nil {
			if _, ok := errors.AsType[*ModelsError](err); ok {
				return nil, err
			}
			return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("Credential store modify failed for %s", providerID), err)
		}
		if post == nil || post.Type != CredentialOAuth {
			return nil, nil
		}
		credential = *post
		// The default window triggers a refresh without imposing a provider
		// contract. Explicit callers (bearer-token export) require the
		// requested minimum after the refresh.
		if minOAuthValidityMs != nil && expiresSoon(credential) {
			return nil, NewModelsError(ModelsErrorOAuth, fmt.Sprintf("OAuth refresh returned a token that expires too soon for %s", providerID), nil)
		}
	}
	auth, err := oauth.ToAuth(credential)
	if err != nil {
		return nil, NewModelsError(ModelsErrorOAuth, fmt.Sprintf("OAuth auth derivation failed for %s", providerID), err)
	}
	return &AuthResult{Auth: auth, Source: "OAuth"}, nil
}

// refreshOAuthWithTimeout bounds a refresh by the caller's context and the
// upstream fifteen-second refresh timeout.
func refreshOAuthWithTimeout(ctx context.Context, oauth *OAuthAuth, credential Credential) (Credential, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, defaultOAuthRefreshTimeout)
	defer cancel()
	return oauth.Refresh(refreshCtx, credential)
}

func resolveAPIKey(ctx context.Context, authContext AuthContext, apiKey *APIKeyAuth, providerID string, credential *Credential) (*AuthResult, error) {
	result, err := apiKey.Resolve(ctx, APIKeyAuthInput{Ctx: authContext, Credential: credential})
	if err != nil {
		return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("API key auth failed for provider %s", providerID), err)
	}
	return result, nil
}

func readProviderCredential(ctx context.Context, credentials CredentialStore, providerID string) (*Credential, error) {
	credential, err := credentials.Read(ctx, providerID)
	if err != nil {
		return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("Credential store read failed for %s", providerID), err)
	}
	return credential, nil
}

// MergeProviderHeaders overlays override onto base, replacing headers
// case-insensitively. Mirrors upstream mergeHeaders in models.ts.
func MergeProviderHeaders(base, override ProviderHeaders) ProviderHeaders {
	if base == nil && override == nil {
		return nil
	}
	merged := make(ProviderHeaders, len(base)+len(override))
	maps.Copy(merged, base)
	for name, value := range override {
		for existing := range merged {
			if strings.EqualFold(existing, name) {
				delete(merged, existing)
			}
		}
		merged[name] = value
	}
	return merged
}
