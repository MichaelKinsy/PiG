package ai

// Ports packages/ai/src/auth/oauth/anthropic.ts.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

const (
	anthropicClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicAuthorizeURL = "https://claude.ai/oauth/authorize"
	anthropicTokenURL     = "https://platform.claude.com/v1/oauth/token"
	anthropicCallbackPort = 53692
	anthropicCallbackPath = "/callback"
	anthropicRedirectURI  = "http://localhost:53692/callback"
	// anthropicCopyCodeRedirectURI is Anthropic's page that shows the authorization code for pasting.
	anthropicCopyCodeRedirectURI = "https://platform.claude.com/oauth/code/callback"
	// AnthropicBrowserLoginMethod and AnthropicCopyCodeLoginMethod are the login method IDs Anthropic login offers.
	AnthropicBrowserLoginMethod  = "browser"
	AnthropicCopyCodeLoginMethod = "copy_code"
	anthropicScopes              = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

// parseAuthorizationInput ECMAScript-trims user-pasted input and extracts its code and state.
func parseAuthorizationInput(input string) (code, state string) {
	v := trimJSWhitespace(input)
	if v == "" {
		return "", ""
	}

	// Try as URL
	if u, err := url.Parse(v); err == nil && u.Scheme != "" {
		return u.Query().Get("code"), u.Query().Get("state")
	}

	// code#state
	if before, after, ok := strings.Cut(v, "#"); ok {
		return before, after
	}

	// code=X&state=Y
	if strings.Contains(v, "code=") {
		q, err := url.ParseQuery(v)
		if err == nil {
			return q.Get("code"), q.Get("state")
		}
	}

	return v, ""
}

type anthropicTokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
}

func exchangeAnthropicCode(ctx context.Context, code, state, verifier, redirectURI string) (OAuthCredentials, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     anthropicClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	})

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, anthropicTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return OAuthCredentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OAuthCredentials{}, fmt.Errorf("token exchange HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tok anthropicTokenResponse
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return OAuthCredentials{}, fmt.Errorf("token exchange invalid JSON: %w", err)
	}

	creds := OAuthCredentials{Refresh: tok.RefreshToken, Access: tok.AccessToken}
	creds.SetExpiresMillis(float64(nowMillis()) + jsnumber.FromJSON(tok.ExpiresIn)*1000 - 5*60*1000)
	return creds, nil
}

// LoginAnthropic runs the Anthropic OAuth authorization code + PKCE flow. The browser callback and the manual prompt
// race; the callback server closes, and the manual prompt is joined, before the token exchange result returns.
// Ports loginAnthropic (.upstream/v1.0.0/packages/ai/src/auth/oauth/anthropic.ts:138).
func LoginAnthropic(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}
	// A busy callback port leaves the manual prompt as the only way to finish.
	callback, err := StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[string]{
		ProviderName: "Anthropic",
		Host:         oauthCallbackHost(),
		Port:         anthropicCallbackPort,
		Path:         anthropicCallbackPath,
		State:        &pkce.Verifier,
		Complete:     func(_ context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		callback = nil
	} else {
		defer callback.Close()
	}

	query := orderedQuery(
		"code", "true",
		"client_id", anthropicClientID,
		"response_type", "code",
		"redirect_uri", anthropicRedirectURI,
		"scope", anthropicScopes,
		"code_challenge", pkce.Challenge,
		"code_challenge_method", "S256",
		"state", pkce.Verifier,
	)
	callbacks.OnAuth(OAuthAuthInfo{
		URL:          anthropicAuthorizeURL + "?" + query,
		Instructions: "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here.",
	})

	result, err := WaitForCallbackOrManualInput(ctx, manualCodeInteraction(callbacks), callback, AuthManualCodePrompt{
		Message:     "Complete login in your browser, or paste the authorization code / redirect URL here:",
		Placeholder: anthropicRedirectURI,
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	var code, state string
	if result.Callback {
		code, state = result.Value, pkce.Verifier
	} else {
		code, state = parseAuthorizationInput(result.Input)
		if state != "" && state != pkce.Verifier {
			return OAuthCredentials{}, fmt.Errorf("OAuth state mismatch")
		}
		if state == "" {
			state = pkce.Verifier
		}
	}
	if code == "" {
		return OAuthCredentials{}, fmt.Errorf("Missing authorization code")
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for tokens...")
	}
	return exchangeAnthropicCode(ctx, code, state, pkce.Verifier, anthropicRedirectURI)
}

// LoginAnthropicCopyCode runs the Anthropic authorization code + PKCE flow without a callback server: Anthropic's page
// shows the code, which the user pastes. It works when the browser runs on another machine.
// Ports loginAnthropicCopyCode (.upstream/v1.0.0/packages/ai/src/auth/oauth/anthropic.ts:191).
func LoginAnthropicCopyCode(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}
	query := orderedQuery(
		"code", "true",
		"client_id", anthropicClientID,
		"response_type", "code",
		"redirect_uri", anthropicCopyCodeRedirectURI,
		"scope", anthropicScopes,
		"code_challenge", pkce.Challenge,
		"code_challenge_method", "S256",
		"state", pkce.Verifier,
	)
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{
			URL:          anthropicAuthorizeURL + "?" + query,
			Instructions: "Complete login in your browser, then copy the code Anthropic shows and paste it here.",
		})
	}

	input, err := manualCodeInteraction(callbacks).Prompt(ctx, AuthManualCodePrompt{
		Message:     "Paste the code Anthropic shows after you sign in:",
		Placeholder: "code#state",
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	code, state := parseAuthorizationInput(input)
	if state != "" && state != pkce.Verifier {
		return OAuthCredentials{}, fmt.Errorf("OAuth state mismatch")
	}
	if code == "" {
		return OAuthCredentials{}, fmt.Errorf("Missing authorization code")
	}
	if state == "" {
		state = pkce.Verifier
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for tokens...")
	}
	return exchangeAnthropicCode(ctx, code, state, pkce.Verifier, anthropicCopyCodeRedirectURI)
}

// RefreshAnthropicToken refreshes an Anthropic OAuth token.
func RefreshAnthropicToken(ctx context.Context, refreshToken string) (OAuthCredentials, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     anthropicClientID,
		"refresh_token": refreshToken,
	})

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, anthropicTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return OAuthCredentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("token refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OAuthCredentials{}, fmt.Errorf("token refresh HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tok anthropicTokenResponse
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return OAuthCredentials{}, fmt.Errorf("token refresh invalid JSON: %w", err)
	}

	creds := OAuthCredentials{Refresh: tok.RefreshToken, Access: tok.AccessToken}
	creds.SetExpiresMillis(float64(nowMillis()) + jsnumber.FromJSON(tok.ExpiresIn)*1000 - 5*60*1000)
	return creds, nil
}

// AnthropicOAuthDisplayName is the OAuth method label upstream renders for
// Claude Pro/Max subscription login.
const AnthropicOAuthDisplayName = "Anthropic (Claude Pro/Max)"

// AnthropicOAuthProvider implements OAuthProviderInterface for Anthropic.
type AnthropicOAuthProvider struct{}

func (AnthropicOAuthProvider) ID() string                          { return "anthropic" }
func (AnthropicOAuthProvider) IsSubscription() bool                { return true }
func (AnthropicOAuthProvider) Name() string                        { return "Anthropic" }
func (AnthropicOAuthProvider) UsesCallbackServer() bool            { return true }
func (AnthropicOAuthProvider) GetAPIKey(c OAuthCredentials) string { return c.Access }

func (a AnthropicOAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return a.LoginContext(context.Background(), callbacks)
}

// LoginMethodPrompt is the login method selection Anthropic login asks first.
func (AnthropicOAuthProvider) LoginMethodPrompt() OAuthSelectPrompt {
	return OAuthSelectPrompt{
		Message: "Select Anthropic login method:",
		Options: []OAuthSelectOption{
			{ID: AnthropicBrowserLoginMethod, Label: "Browser login (default)"},
			{ID: AnthropicCopyCodeLoginMethod, Label: "Copy code login (headless)"},
		},
	}
}

// LoginContext asks for the browser or copy code login method, then runs it. Without a select callback it runs browser
// login, the default. Ports anthropicOAuth.login (.upstream/v1.0.0/packages/ai/src/auth/oauth/anthropic.ts:273).
func (a AnthropicOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	prompt := a.LoginMethodPrompt()
	method := AnthropicBrowserLoginMethod
	var err error
	switch {
	case callbacks.OnSelectContext != nil:
		method, err = callbacks.OnSelectContext(ctx, prompt)
	case callbacks.OnSelect != nil:
		method, err = callbacks.OnSelect(prompt)
	}
	if err != nil {
		return OAuthCredentials{}, err
	}
	switch method {
	case AnthropicCopyCodeLoginMethod:
		return LoginAnthropicCopyCode(ctx, callbacks)
	case AnthropicBrowserLoginMethod:
		return LoginAnthropic(ctx, callbacks)
	}
	return OAuthCredentials{}, fmt.Errorf("Unknown Anthropic login method: %s", method)
}

func (a AnthropicOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return a.RefreshTokenContext(context.Background(), creds)
}

func (AnthropicOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	return RefreshAnthropicToken(ctx, creds.Refresh)
}
