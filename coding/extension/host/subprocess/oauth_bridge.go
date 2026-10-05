package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// OAuth extension bridge: a subprocess extension that declares an oauth
// sub-config in its provider registration gets an ai.OAuthProviderInterface
// proxy whose methods RPC oauth_* requests into the extension. See protocol.go
// and docs/extension-authoring.md for the wire contract.

// Upstream awaits the provider's login, refreshToken, and getApiKey with no
// deadline and lets their exceptions propagate. The proxy does the same: the
// caller's context is the only bound, and the D56 heartbeat fails a request to
// an extension that stops responding.

// oauthProxy implements ai.OAuthProviderInterface by RPCing into a subprocess
// extension over its existing connection.
type oauthProxy struct {
	host *Host
	me   *managedExt
	name string
	cfg  ProviderOAuthConfig

	keyMu     sync.Mutex
	keyCreds  string
	keyValue  string
	keyCached bool
}

// oauthProxyWithStore additionally implements ai.OAuthCredentialStore. Only
// registered when the extension sets has_credential_store, so a type assertion
// on ai.OAuthCredentialStore succeeds exactly for extensions that own their
// credential store (the rest fall back to core auth.json persistence).
type oauthProxyWithStore struct {
	*oauthProxy
}

func (p *oauthProxy) ID() string { return p.name }

func (p *oauthProxy) Name() string {
	if p.cfg.Name != "" {
		return p.cfg.Name
	}
	return p.name
}

func (p *oauthProxy) UsesCallbackServer() bool { return false }

// IsSubscription reads registration metadata without calling the extension.
func (p *oauthProxy) IsSubscription() bool { return p.cfg.IsSubscription }

func (p *oauthProxy) Login(cb ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return p.LoginContext(context.Background(), cb)
}

// LoginContext runs the extension's login flow until it completes or ctx ends.
func (p *oauthProxy) LoginContext(ctx context.Context, cb ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	// Publish the host-supplied callbacks so the concurrent oauth.cb.* calls the
	// extension issues during this login can be routed back to them. The session
	// is keyed by extension name, not provider name: the oauth.cb.* calls carry
	// only the issuing extension's identity, so handleOAuthCallback looks them up
	// by extension name. An extension serving several providers therefore runs
	// one login at a time, which matches the single interactive /login flow.
	sessionKey := p.me.config.Name
	p.host.setOAuthLoginSession(sessionKey, cb)
	defer p.host.clearOAuthLoginSession(sessionKey)

	resp, err := p.request(ctx, MethodOAuthLogin, nil)
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	var wire OAuthCredentialsWire
	if err := unmarshalOAuthResult(resp, &wire); err != nil {
		return ai.OAuthCredentials{}, err
	}
	return wire, nil
}

func (p *oauthProxy) RefreshToken(creds ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return p.RefreshTokenContext(context.Background(), creds)
}

// RefreshTokenContext refreshes credentials until the extension answers or ctx ends.
func (p *oauthProxy) RefreshTokenContext(ctx context.Context, creds ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	if !p.cfg.HasRefresh {
		return ai.OAuthCredentials{}, fmt.Errorf("%s: token refresh not supported", p.name)
	}
	args, err := storedOAuthCredential(creds)
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	// Pi hands a stored-credential refresh AbortSignal.timeout alone (auth/resolve.ts:refreshStoredOAuthCredential): the caller's cancellation never reaches the extension. A refresh with any other context carries that context's cancellation.
	signalCtx, req := ctx, RequestPayload{Method: MethodOAuthRefresh, Tool: p.name, Args: args}
	if timeout, timeoutOnly := ai.OAuthRefreshTimeout(ctx); timeoutOnly {
		signalCtx = context.WithoutCancel(ctx)
		milliseconds := float64(timeout.Milliseconds())
		req.SignalTimeoutMS = &milliseconds
	}
	conn := p.me.connection()
	if conn == nil {
		return ai.OAuthCredentials{}, errors.New("extension not connected")
	}
	id := conn.newRequestID()
	resp, err := p.requestOn(ctx, conn, id, req)
	// The extension may keep the signal after it answers; the caller's later abort still reaches it. A request that ended with the caller's own cancellation was cancelled in flight.
	if ctx.Err() == nil {
		conn.forwardAbort(signalCtx, id)
	}
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	var wire OAuthCredentialsWire
	if err := unmarshalOAuthResult(resp, &wire); err != nil {
		return ai.OAuthCredentials{}, err
	}
	p.invalidateKeyCache()
	return wire, nil
}

// GetAPIKey extracts the bearer from credentials. It is called at key-resolution
// time (not per token), and the result is deterministic in creds, so it is
// cached by credential value to avoid re-RPCing within a session. When the
// extension does not override extraction, the access token is used directly.
//
// Key resolution calls GetAPIKeyContext, which reports the extension's
// failure. GetAPIKey exists for the string-only interface and returns "" on
// failure.
func (p *oauthProxy) GetAPIKey(creds ai.OAuthCredentials) string {
	key, _ := p.GetAPIKeyContext(context.Background(), creds)
	return key
}

// GetAPIKeyContext resolves the bearer, returning the extension's error when
// its getApiKey fails, as upstream propagates the exception to the model call.
func (p *oauthProxy) GetAPIKeyContext(ctx context.Context, creds ai.OAuthCredentials) (string, error) {
	if !p.cfg.HasGetAPIKey {
		return creds.Access, nil
	}
	// getApiKey may read any credential key, so the cache is keyed by the complete stored credential.
	args, err := storedOAuthCredential(creds)
	if err != nil {
		return "", err
	}
	wire := string(args)
	p.keyMu.Lock()
	if p.keyCached && p.keyCreds == wire {
		v := p.keyValue
		p.keyMu.Unlock()
		return v, nil
	}
	p.keyMu.Unlock()

	resp, err := p.request(ctx, MethodOAuthGetAPIKey, args)
	if err != nil {
		return "", err
	}
	var out OAuthAPIKeyResult
	if err := unmarshalOAuthResult(resp, &out); err != nil {
		return "", fmt.Errorf("%s %s: %w", p.name, MethodOAuthGetAPIKey, err)
	}
	p.keyMu.Lock()
	p.keyCreds, p.keyValue, p.keyCached = wire, out.APIKey, true
	p.keyMu.Unlock()
	return out.APIKey, nil
}

func (p *oauthProxy) invalidateKeyCache() {
	p.keyMu.Lock()
	p.keyCached = false
	p.keyValue = ""
	p.keyMu.Unlock()
}

func (p *oauthProxyWithStore) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	resp, err := p.request(context.Background(), MethodOAuthCredentialStatus, nil)
	if err != nil {
		return ai.OAuthCredentialStatus{}, false
	}
	var out OAuthCredentialStatusResult
	if err := unmarshalOAuthResult(resp, &out); err != nil || !out.Present {
		return ai.OAuthCredentialStatus{}, false
	}
	return ai.OAuthCredentialStatus{AuthType: out.AuthType, Source: out.Source}, true
}

func (p *oauthProxyWithStore) StoreOAuthCredentials(creds ai.OAuthCredentials) (string, error) {
	resp, err := p.request(context.Background(), MethodOAuthStoreCredentials, creds)
	if err != nil {
		return "", err
	}
	var out OAuthStoreResult
	if err := unmarshalOAuthResult(resp, &out); err != nil {
		return "", err
	}
	p.invalidateKeyCache()
	return out.Path, nil
}

func (p *oauthProxyWithStore) DeleteOAuthCredentials() (bool, error) {
	resp, err := p.request(context.Background(), MethodOAuthDeleteCredentials, nil)
	if err != nil {
		return false, err
	}
	var out OAuthDeleteResult
	if err := unmarshalOAuthResult(resp, &out); err != nil {
		return false, err
	}
	p.invalidateKeyCache()
	return out.Deleted, nil
}

func (p *oauthProxy) request(ctx context.Context, method string, payload any) (*Envelope, error) {
	conn := p.me.connection()
	if conn == nil {
		return nil, errors.New("extension not connected")
	}
	var args json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		args = b
	}
	// Tool carries the provider name so an extension serving several OAuth
	// providers can route the request to the right one; oauth_* methods do not
	// otherwise identify the provider.
	return p.requestOn(ctx, conn, "", RequestPayload{Method: method, Tool: p.name, Args: args})
}

// requestOn sends req on conn under id, or a fresh ID when id is empty.
func (p *oauthProxy) requestOn(ctx context.Context, conn *Conn, id string, req RequestPayload) (*Envelope, error) {
	resp, err := conn.Request(ctx, &Envelope{ID: id, Type: MsgRequest, Request: &req})
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", p.name, req.Method, err)
	}
	if resp.Response != nil && resp.Response.Error != nil {
		return nil, resp.Response.Error.ToError()
	}
	return resp, nil
}

func unmarshalOAuthResult(resp *Envelope, out any) error {
	if resp.Response == nil || len(resp.Response.Result) == 0 {
		return errors.New("empty oauth response")
	}
	return json.Unmarshal(resp.Response.Result, out)
}

// storedOAuthCredential returns the stored credential object Pi passes to refreshToken and getApiKey: the token object with its "oauth" discriminator (provider-composer.ts:292-293, auth/resolve.ts:145-155).
func storedOAuthCredential(creds ai.OAuthCredentials) (json.RawMessage, error) {
	credential, err := ai.CredentialFromOAuth(creds)
	if err != nil {
		return nil, err
	}
	return json.Marshal(credential)
}

// registerOAuthProvider inspects a provider's config for an oauth sub-config
// and, when present, registers a bridged ai OAuth provider driven by the current connection.
// Called from both the isolated and packed register paths. A nil/absent oauth
// key is a no-op.
func (h *Host) registerOAuthProvider(me *managedExt, name string, config json.RawMessage) error {
	var probe struct {
		OAuth *ProviderOAuthConfig `json:"oauth"`
	}
	if err := json.Unmarshal(config, &probe); err != nil {
		return fmt.Errorf("provider %s: decode oauth config: %w", name, err)
	}
	if probe.OAuth == nil {
		return nil
	}
	base := &oauthProxy{host: h, me: me, name: name, cfg: *probe.OAuth}
	var provider ai.OAuthProviderInterface = base
	if probe.OAuth.HasCredentialStore {
		provider = &oauthProxyWithStore{oauthProxy: base}
	}
	ai.RegisterOAuthProvider(name, provider)
	h.mu.Lock()
	if !slices.Contains(me.oauthProviderNames, name) {
		me.oauthProviderNames = append(me.oauthProviderNames, name)
	}
	h.mu.Unlock()
	return nil
}

// unregisterOAuthProviders removes this extension's bridged OAuth providers,
// skipping any a replacement extension is keeping.
func (h *Host) unregisterOAuthProviders(me *managedExt, keep map[string]struct{}) {
	for _, name := range me.oauthProviderNames {
		if _, k := keep[name]; k {
			continue
		}
		ai.UnregisterOAuthProvider(name)
	}
}

type oauthLoginSession struct {
	OnAuth            func(ai.OAuthAuthInfo)
	OnDeviceCode      func(ai.OAuthDeviceCodeInfo)
	OnPrompt          func(context.Context, ai.OAuthPrompt) (string, error)
	OnProgress        func(string)
	OnManualCodeInput func(context.Context) (string, error)
	OnSelect          func(context.Context, ai.OAuthSelectPrompt) (string, error)
}

func initiatedOAuthLoginSession(callbacks ai.OAuthLoginCallbacks) oauthLoginSession {
	return oauthLoginSession{
		OnAuth:       callbacks.OnAuth,
		OnDeviceCode: callbacks.OnDeviceCode,
		OnProgress:   callbacks.OnProgress,
		OnPrompt: func(ctx context.Context, prompt ai.OAuthPrompt) (string, error) {
			if callbacks.OnPromptContext != nil {
				return callbacks.OnPromptContext(ctx, prompt)
			}
			if callbacks.OnPrompt == nil {
				return "", errors.New("no prompt handler")
			}
			value, err := callbacks.OnPrompt(prompt)
			extension.CallInitiated(ctx)
			return value, err
		},
		OnSelect: func(ctx context.Context, prompt ai.OAuthSelectPrompt) (string, error) {
			if callbacks.OnSelectContext != nil {
				return callbacks.OnSelectContext(ctx, prompt)
			}
			if callbacks.OnSelect == nil {
				return "", errors.New("no select handler")
			}
			value, err := callbacks.OnSelect(prompt)
			extension.CallInitiated(ctx)
			return value, err
		},
		OnManualCodeInput: func(ctx context.Context) (string, error) {
			if callbacks.OnManualCodeInputContext != nil {
				return callbacks.OnManualCodeInputContext(ctx)
			}
			if callbacks.OnManualCodeInput == nil {
				return "", errors.New("no manual-code handler")
			}
			value, err := callbacks.OnManualCodeInput()
			extension.CallInitiated(ctx)
			return value, err
		},
	}
}

func (h *Host) setOAuthLoginSession(name string, cb ai.OAuthLoginCallbacks) {
	h.oauthLoginMu.Lock()
	if h.oauthLoginSessions == nil {
		h.oauthLoginSessions = map[string]oauthLoginSession{}
	}
	h.oauthLoginSessions[name] = initiatedOAuthLoginSession(cb)
	h.oauthLoginMu.Unlock()
}

func (h *Host) clearOAuthLoginSession(name string) {
	h.oauthLoginMu.Lock()
	delete(h.oauthLoginSessions, name)
	h.oauthLoginMu.Unlock()
}

func (h *Host) oauthLoginSession(name string) (oauthLoginSession, bool) {
	h.oauthLoginMu.Lock()
	cb, ok := h.oauthLoginSessions[name]
	h.oauthLoginMu.Unlock()
	return cb, ok
}

// handleOAuthCallback routes an oauth.cb.* ext→host call to the login callbacks
// the proxy's in-flight Login published. Value-returning callbacks reply with an
// OAuthInputResult; a handler error is reported as a user cancel so the login
// flow stops cleanly rather than crashing.
func (h *Host) handleOAuthCallback(ctx context.Context, extName string, call *CallPayload) (*CallResultPayload, error) {
	cb, ok := h.oauthLoginSession(extName)
	if !ok {
		return nil, fmt.Errorf("no active oauth login for %s", extName)
	}
	switch call.Method {
	case CallOAuthOnAuth:
		var w OAuthAuthInfoWire
		if err := json.Unmarshal(call.Args, &w); err != nil {
			return nil, err
		}
		if cb.OnAuth != nil {
			cb.OnAuth(ai.OAuthAuthInfo{URL: w.URL, Instructions: w.Instructions})
		}
		return &CallResultPayload{}, nil
	case CallOAuthOnDeviceCode:
		var w OAuthDeviceCodeInfoWire
		if err := json.Unmarshal(call.Args, &w); err != nil {
			return nil, err
		}
		if cb.OnDeviceCode != nil {
			cb.OnDeviceCode(ai.OAuthDeviceCodeInfo{
				UserCode:         w.UserCode,
				VerificationURI:  w.VerificationURI,
				IntervalSeconds:  w.IntervalSeconds,
				ExpiresInSeconds: w.ExpiresInSeconds,
			})
		}
		return &CallResultPayload{}, nil
	case CallOAuthOnProgress:
		var w OAuthProgressWire
		if err := json.Unmarshal(call.Args, &w); err != nil {
			return nil, err
		}
		if cb.OnProgress != nil {
			cb.OnProgress(w.Message)
		}
		return &CallResultPayload{}, nil
	case CallOAuthOnPrompt:
		var w OAuthPromptWire
		if err := json.Unmarshal(call.Args, &w); err != nil {
			return nil, err
		}
		return oauthInputResult(func() (string, error) {
			return cb.OnPrompt(ctx, ai.OAuthPrompt{Message: w.Message, Placeholder: w.Placeholder, AllowEmpty: w.AllowEmpty})
		})
	case CallOAuthOnSelect:
		var w OAuthSelectPromptWire
		if err := json.Unmarshal(call.Args, &w); err != nil {
			return nil, err
		}
		opts := make([]ai.OAuthSelectOption, len(w.Options))
		for i, o := range w.Options {
			opts[i] = ai.OAuthSelectOption{ID: o.ID, Label: o.Label}
		}
		return oauthInputResult(func() (string, error) {
			return cb.OnSelect(ctx, ai.OAuthSelectPrompt{Message: w.Message, Options: opts})
		})
	case CallOAuthOnManualCodeInput:
		return oauthInputResult(func() (string, error) {
			return cb.OnManualCodeInput(ctx)
		})
	default:
		return nil, fmt.Errorf("unknown oauth callback %q", call.Method)
	}
}

func oauthInputResult(fn func() (string, error)) (*CallResultPayload, error) {
	val, err := fn()
	if err != nil {
		b, _ := json.Marshal(OAuthInputResult{Cancel: true})
		return &CallResultPayload{Result: b}, nil
	}
	b, _ := json.Marshal(OAuthInputResult{Value: val})
	return &CallResultPayload{Result: b}, nil
}
