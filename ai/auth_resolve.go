package ai

// Request-auth resolution. Mirrors upstream packages/ai/src/auth/types.ts
// (ModelAuth, AuthResult, AuthCheck, AuthContext, ApiKeyAuth, OAuthAuth,
// ProviderAuth), packages/ai/src/auth/resolve.ts (resolveProviderAuth,
// ModelsError), packages/ai/src/auth/context.ts (defaultProviderAuthContext)
// and the checkAuth/getAuth paths of packages/ai/src/models.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"strings"
	"sync"
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
	// SourcePresent preserves an explicitly empty source received over JSON.
	SourcePresent bool `json:"-"`
}

func (r *AuthResult) UnmarshalJSON(data []byte) error {
	type plain AuthResult
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	_, decoded.SourcePresent = fields["source"]
	*r = AuthResult(decoded)
	return nil
}

// AuthCheck reports that auth is configured without resolving it.
type AuthCheck struct {
	Source string
	Type   CredentialType
}

// AuthCheckType is the credential type the check found, "oauth" or "api_key" (the provider selector compares it with the option's auth type).
func (c *AuthCheck) AuthCheckType() string { return string(c.Type) }

// AuthCheckSource is where the configured auth comes from, as the provider selector prints it.
func (c *AuthCheck) AuthCheckSource() string { return c.Source }

// AuthContext is environment access for auth resolution. Env reports a
// value only when it is set and not blank.
type AuthContext struct {
	Env        func(name string) (string, bool)
	FileExists func(path string) bool
}

// DefaultProviderAuthContext reads process environment variables and checks files on disk. It treats ECMAScript-trimmed empty values as absent without trimming nonblank values. Mirrors upstream defaultProviderAuthContext.
func DefaultProviderAuthContext() AuthContext {
	return AuthContext{
		Env: func(name string) (string, bool) {
			value, ok := os.LookupEnv(name)
			if !ok || trimJSWhitespace(value) == "" {
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
	Name string
	// Login performs provider-owned setup. Nil denotes ambient-only authentication.
	Login   func(context.Context, AuthInteraction) (Credential, error)
	Check   func(ctx context.Context, input APIKeyAuthInput) (*AuthCheck, error)
	Resolve func(ctx context.Context, input APIKeyAuthInput) (*AuthResult, error)
}

// AuthMethodName is the method's display name, which the provider selector searches (oauth-selector.ts:136 `provider.method?.name`).
func (a *APIKeyAuth) AuthMethodName() string { return a.Name }

// LoginOptions is app-supplied context for Models.Login.
type LoginOptions struct {
	// GetDeviceID returns the stable ID of this app installation, for example sent to OpenAI as its agent host ID.
	// Login flows call it only when they need it, so apps can create the ID on first use; it must return the same ID on
	// every later call. Nil means the app supplies none.
	GetDeviceID func() string
	// AgentName is the name this app introduces itself with during login, for example OpenAI's agent name hint and the Codex originator. Nil means the flow's own default; an empty name is kept as given (auth/types.ts:206-213).
	AgentName *string
}

// OAuthAuth refreshes stored OAuth credentials and derives request auth
// from them.
type OAuthAuth struct {
	Name           string
	IsSubscription bool
	LoginLabel     string
	Login          func(context.Context, AuthInteraction, LoginOptions) (Credential, error)
	Refresh        func(ctx context.Context, credential Credential) (Credential, error)
	ToAuth         func(credential Credential) (ModelAuth, error)
	// store is the D40 extension-owned credential store of the OAuth provider whose flow Login runs; a login through it saves there instead of the core credential store.
	store OAuthCredentialStore
}

// AuthMethodName is the method's display name, which the provider selector searches (oauth-selector.ts:136 `provider.method?.name`).
func (a *OAuthAuth) AuthMethodName() string { return a.Name }

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

// Name is the upstream `name` property.
func (*ModelsError) Name() string { return "ModelsError" }

// NewModelsError mirrors the upstream ModelsError constructor, including ECMAScript trimming of the cause detail.
func NewModelsError(code ModelsErrorCode, message string, cause error) *ModelsError {
	if cause != nil {
		if detail := trimJSWhitespace(cause.Error()); detail != "" && !strings.Contains(message, detail) {
			message = message + ": " + detail
		}
	}
	e := &ModelsError{Code: code, Message: message, Cause: cause}
	return e
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
	credential, err := refreshStoredOAuth(ctx, credentials, providerID, oauth, stored, minOAuthValidityMs)
	if err != nil || credential == nil {
		return nil, err
	}
	auth, err := oauth.ToAuth(*credential)
	if err != nil {
		return nil, NewModelsError(ModelsErrorOAuth, fmt.Sprintf("OAuth auth derivation failed for %s", providerID), err)
	}
	return &AuthResult{Auth: auth, Source: "OAuth"}, nil
}

// refreshStoredOAuth is resolveStoredOAuth's refresh step: it returns the credential to derive auth from, refreshed under the store's lock when it expires within the minimum validity, or nil when the credential was removed or replaced by a non-OAuth one meanwhile.
func refreshStoredOAuth(ctx context.Context, credentials CredentialStore, providerID string, oauth *OAuthAuth, stored Credential, minOAuthValidityMs *float64) (*Credential, error) {
	minimumValidityMs := defaultOAuthMinimumValidityMs
	if minOAuthValidityMs != nil {
		minimumValidityMs = math.Max(minimumValidityMs, *minOAuthValidityMs)
	}
	expiresSoon := func(credential Credential) bool {
		return float64(nowMillis())+minimumValidityMs >= credential.ExpiresMillis()
	}
	credential := stored
	if expiresSoon(credential) {
		// Optimistic check said expired; the authoritative check runs under the lock.
		post, err := refreshStoredOAuthCredential(ctx, credentials, providerID, oauth, expiresSoon)
		if err != nil {
			return nil, err
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
	return &credential, nil
}

// oauthRefreshWork owns the stored-credential refreshes that outlive a cancelled caller. Each is bounded by the store's lock and the fifteen-second refresh timeout.
var oauthRefreshWork sync.WaitGroup

// refreshStoredOAuthCredential refreshes a stored OAuth credential under the credential store's lock and persists the result before the lock is released. needsRefresh is re-checked under the lock, so concurrent callers and processes refresh once.
// ctx cancels only the wait for the lock. Once a refresh starts, the provider may already have rotated the refresh token, so the refresh and its persistence ignore ctx and are bounded only by the fifteen-second timeout; otherwise a cancelled caller could discard the only valid refresh token. Upstream's callers race the promise with their signal; this call does that itself, so a cancelled caller returns the cancellation cause while the refresh finishes and persists in the background. It returns nil when the credential was removed or replaced by a non-OAuth one meanwhile.
// upstream: packages/ai/src/auth/resolve.ts:refreshStoredOAuthCredential
func refreshStoredOAuthCredential(ctx context.Context, credentials CredentialStore, providerID string, oauth *OAuthAuth, needsRefresh func(Credential) bool) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	type outcome struct {
		credential *Credential
		err        error
	}
	done := make(chan outcome, 1)
	oauthRefreshWork.Go(func() {
		credential, err := refreshStoredOAuthLocked(ctx, credentials, providerID, oauth, needsRefresh)
		done <- outcome{credential, err}
	})
	select {
	case result := <-done:
		return result.credential, result.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func refreshStoredOAuthLocked(ctx context.Context, credentials CredentialStore, providerID string, oauth *OAuthAuth, needsRefresh func(Credential) bool) (*Credential, error) {
	lockWait, cancelLockWait := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancelLockWait(nil)
	stopLockWait := context.AfterFunc(ctx, func() { cancelLockWait(context.Cause(ctx)) })
	defer stopLockWait()
	post, err := credentials.Modify(lockWait, providerID, func(current *Credential) (*Credential, error) {
		stopLockWait()
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}
		if current == nil || current.Type != CredentialOAuth || !needsRefresh(*current) {
			return nil, nil // logged out meanwhile, or another process or request refreshed
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
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("Credential store modify failed for %s", providerID), err)
	}
	if post == nil || post.Type != CredentialOAuth {
		return nil, nil
	}
	return post, nil
}

// refreshOAuthWithTimeout refreshes with a signal bounded only by the fifteen-second timeout, which stays live through refresh settlement and GC: ctx's cancellation does not reach the provider.
// upstream: packages/ai/src/auth/resolve.ts:refreshStoredOAuthCredential (AbortSignal.timeout(DEFAULT_OAUTH_REFRESH_TIMEOUT_MS))
func refreshOAuthWithTimeout(ctx context.Context, oauth *OAuthAuth, credential Credential) (Credential, error) {
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultOAuthRefreshTimeout)
	// Cleanup follows the timeout; refresh return and Context-wrapper collection are not cancellation events.
	context.AfterFunc(refreshCtx, cancel)
	return oauth.Refresh(WithOAuthRefreshTimeout(refreshCtx, defaultOAuthRefreshTimeout), credential)
}

type oauthRefreshTimeoutKey struct{}

// WithOAuthRefreshTimeout marks the context of a provider refresh whose signal Pi builds as AbortSignal.timeout(timeout): the caller's cancellation does not reach it (auth/resolve.ts:refreshStoredOAuthCredential). A provider that hands its callback a signal, such as an extension bridge, reads the timeout with OAuthRefreshTimeout.
func WithOAuthRefreshTimeout(ctx context.Context, timeout time.Duration) context.Context {
	return context.WithValue(ctx, oauthRefreshTimeoutKey{}, timeout)
}

// OAuthRefreshTimeout returns the timeout that alone bounds the refresh signal, or ok false when the refresh signal is a caller's cancellation.
func OAuthRefreshTimeout(ctx context.Context) (timeout time.Duration, ok bool) {
	timeout, ok = ctx.Value(oauthRefreshTimeoutKey{}).(time.Duration)
	return timeout, ok
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
