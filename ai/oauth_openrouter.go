package ai

// Mirrors upstream .upstream/current/packages/ai/src/auth/oauth/openrouter.ts.
//
// OpenRouter exchanges an authorization code for a permanent, user-controlled
// API key rather than an expiring access/refresh token pair. The callback is
// handled by a one-shot loopback server on an ephemeral port, raced against a
// manual paste prompt so remote/headless sessions can paste the redirect URL
// when the browser cannot reach the loopback server.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	openRouterAuthorizeURL = "https://openrouter.ai/auth"
	openRouterTokenURL     = "https://openrouter.ai/api/v1/auth/keys"

	openRouterLoginTimeout         = 5 * time.Minute
	openRouterTokenExchangeTimeout = 30 * time.Second

	// openRouterMaxSafeInteger mirrors the JavaScript Number.MAX_SAFE_INTEGER
	// upstream stores as the credential expiry: an OpenRouter key never
	// expires on its own, so the runtime must never treat it as stale.
	openRouterMaxSafeInteger int64 = 1<<53 - 1
)

// openRouterCallbackHost mirrors upstream getCallbackHost(): honor
// PI_OAUTH_CALLBACK_HOST, otherwise bind loopback.
func openRouterCallbackHost() string { return oauthCallbackHost() }

// parseOpenRouterAuthorizationInput extracts the authorization code from a
// pasted redirect URL, a `code=`-bearing query fragment, or a bare code.
// Mirrors upstream parseAuthorizationInput.
func parseOpenRouterAuthorizationInput(input string) string {
	v := trimJSWhitespace(input)
	if v == "" {
		return ""
	}
	// A full URL (has a scheme): pull the code query parameter. Go's url.Parse
	// is lenient, so the scheme check emulates JS `new URL(value)` throwing on a
	// bare string.
	if u, err := url.Parse(v); err == nil && u.Scheme != "" {
		return u.Query().Get("code")
	}
	if strings.Contains(v, "code=") {
		if q, err := url.ParseQuery(v); err == nil {
			return q.Get("code")
		}
	}
	return v
}

// openRouterErrorDetail extracts a human-readable detail from a token error
// body, mirroring upstream errorDetail's precedence.
func openRouterErrorDetail(body map[string]any) string {
	if s, ok := body["error_description"].(string); ok {
		return s
	}
	if s, ok := body["message"].(string); ok {
		return s
	}
	if s, ok := body["error"].(string); ok {
		return s
	}
	if nested, ok := body["error"].(map[string]any); ok {
		if s, ok := nested["message"].(string); ok {
			return s
		}
	}
	return ""
}

// exchangeOpenRouterCode posts the authorization code and PKCE verifier to
// OpenRouter and returns a permanent API key credential.
func exchangeOpenRouterCode(ctx context.Context, code, verifier string) (OAuthCredentials, error) {
	if ctx.Err() != nil {
		return OAuthCredentials{}, errors.New("Login cancelled")
	}
	reqBody, _ := json.Marshal(map[string]string{
		"code":                  code,
		"code_verifier":         verifier,
		"code_challenge_method": "S256",
	})

	ctx2, cancel := context.WithTimeout(ctx, openRouterTokenExchangeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, openRouterTokenURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return OAuthCredentials{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return OAuthCredentials{}, errors.New("Login cancelled")
		}
		if ctx2.Err() != nil {
			return OAuthCredentials{}, errors.New("OpenRouter OAuth token exchange timed out")
		}
		return OAuthCredentials{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	// Upstream keeps a JSON object body, ignores arrays/scalars, and only treats
	// a parse failure as fatal when the HTTP status was otherwise successful.
	var raw any
	parseErr := json.Unmarshal(respBody, &raw)
	body, _ := raw.(map[string]any)
	if body == nil {
		body = map[string]any{}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf("OpenRouter OAuth key exchange failed (HTTP %d)", resp.StatusCode)
		if detail := openRouterErrorDetail(body); detail != "" {
			msg += ": " + detail
		}
		return OAuthCredentials{}, errors.New(msg)
	}
	if parseErr != nil {
		return OAuthCredentials{}, errors.New("OpenRouter OAuth returned invalid JSON")
	}
	key, _ := body["key"].(string)
	if key == "" {
		return OAuthCredentials{}, errors.New(`OpenRouter OAuth response carries no "key"`)
	}
	return OAuthCredentials{Access: key, Refresh: "", Expires: openRouterMaxSafeInteger}, nil
}

// LoginOpenRouter runs the OpenRouter OAuth PKCE flow: a loopback callback raced against a manual paste. OpenRouter
// sends no state; the random path keeps stray requests from completing the sign-in. A claimed browser callback always
// wins over a racing paste.
// Ports loginOpenRouter (.upstream/v0.99.1/packages/ai/src/auth/oauth/openrouter.ts:113).
func LoginOpenRouter(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}
	callback, err := StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[OAuthCredentials]{
		ProviderName: "OpenRouter",
		Host:         openRouterCallbackHost(),
		Port:         0,
		Path:         "/oauth/callback/" + uuid.NewString(),
		Complete: func(ctx context.Context, code string) (OAuthCredentials, error) {
			return exchangeOpenRouterCode(ctx, code, pkce.Verifier)
		},
		Timeout: openRouterLoginTimeout,
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	defer callback.Close()

	query := orderedQuery(
		"callback_url", callback.RedirectURI,
		"code_challenge", pkce.Challenge,
		"code_challenge_method", "S256",
	)
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Listening for OpenRouter OAuth callback on " + callback.RedirectURI)
	}
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{
			URL:          openRouterAuthorizeURL + "?" + query,
			Instructions: "Complete sign-in in your browser. If the browser is on another machine, paste the final redirect URL here.",
		})
	}

	result, err := WaitForCallbackOrManualInput(ctx, manualCodeInteraction(callbacks), callback, AuthManualCodePrompt{
		Message:     "Complete sign-in in your browser, or paste the authorization code / redirect URL here:",
		Placeholder: callback.RedirectURI,
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	if result.Callback {
		return result.Value, nil
	}
	code := parseOpenRouterAuthorizationInput(result.Input)
	if code == "" {
		return OAuthCredentials{}, errors.New("Missing authorization code")
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for an API key...")
	}
	return exchangeOpenRouterCode(ctx, code, pkce.Verifier)
}

// OpenRouterOAuthProvider implements OAuthProviderInterface for OpenRouter.
//
// Unlike the other built-in OAuth providers, OpenRouter is not a subscription
// login: its flow yields a permanent API key, and upstream openRouterOAuth
// leaves isSubscription unset.
type OpenRouterOAuthProvider struct{}

func (OpenRouterOAuthProvider) ID() string                          { return "openrouter" }
func (OpenRouterOAuthProvider) Name() string                        { return "OpenRouter" }
func (OpenRouterOAuthProvider) UsesCallbackServer() bool            { return true }
func (OpenRouterOAuthProvider) GetAPIKey(c OAuthCredentials) string { return c.Access }

func (p OpenRouterOAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.LoginContext(context.Background(), callbacks)
}

func (OpenRouterOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return LoginOpenRouter(ctx, callbacks)
}

// RefreshToken returns the credential unchanged: an OpenRouter API key does not
// expire and has no refresh token. Mirrors upstream openRouterOAuth.refresh.
func (p OpenRouterOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return p.RefreshTokenContext(context.Background(), creds)
}

func (OpenRouterOAuthProvider) RefreshTokenContext(_ context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	return creds, nil
}
