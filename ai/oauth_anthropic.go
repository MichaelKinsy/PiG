package ai

// Ports packages/ai/src/auth/oauth/anthropic.ts.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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
	anthropicScopes       = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
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

type callbackResult struct {
	Code  string
	State string
}

// startCallbackServer starts a local HTTP server for the OAuth callback.
func startCallbackServer(expectedState string) (srv *http.Server, listener net.Listener, resultCh chan *callbackResult, serveDone chan struct{}, err error) {
	resultCh = make(chan *callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(anthropicCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, OAuthErrorHTML("Anthropic authentication did not complete.", "Error: "+errParam))
			return
		}
		code := q.Get("code")
		state := q.Get("state")
		if code == "" || state == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, OAuthErrorHTML("Missing code or state parameter.", ""))
			return
		}
		if state != expectedState {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, OAuthErrorHTML("State mismatch.", ""))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, OAuthSuccessHTML("Anthropic authentication completed. You can close this window."))
		select {
		case resultCh <- &callbackResult{Code: code, State: state}: // upstream: ai/src/auth/oauth/anthropic.ts:settled
		default:
		}
	})

	srv = &http.Server{Handler: mux}
	listener, err = net.Listen("tcp", net.JoinHostPort(firstNonEmptyString(os.Getenv("PI_OAUTH_CALLBACK_HOST"), "127.0.0.1"), strconv.Itoa(anthropicCallbackPort)))
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("listen on port %d: %w", anthropicCallbackPort, err)
	}

	serveDone = make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(listener) // returns on Shutdown
	}()

	return srv, listener, resultCh, serveDone, nil
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

// LoginAnthropic runs the Anthropic OAuth authorization code + PKCE flow.
// Authorization cleanup cancels and joins contextual manual prompts and closes the callback listener while the token exchange is pending.
func LoginAnthropic(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}

	srv, listener, resultCh, serveDone, err := startCallbackServer(pkce.Verifier)
	if err != nil {
		return OAuthCredentials{}, err
	}
	var cancelManual context.CancelFunc
	var manualDone chan struct{}
	finishAuthorization := sync.OnceFunc(func() {
		if cancelManual != nil {
			cancelManual()
			if callbacks.OnManualCodeInputContext != nil {
				<-manualDone
			}
		}
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		// Shutdown can precede Serve registering its listener; close the owned listener explicitly.
		_ = listener.Close()
		_ = srv.Shutdown(shutCtx)
		<-serveDone
	})
	defer finishAuthorization()

	params := url.Values{
		"code":                  {"true"},
		"client_id":             {anthropicClientID},
		"response_type":         {"code"},
		"redirect_uri":          {anthropicRedirectURI},
		"scope":                 {anthropicScopes},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {pkce.Verifier},
	}
	authURL := anthropicAuthorizeURL + "?" + params.Encode()

	callbacks.OnAuth(OAuthAuthInfo{
		URL:          authURL,
		Instructions: "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here.",
	})

	var code, state string
	redirectURI := anthropicRedirectURI

	// Wait for callback or manual input
	if callbacks.OnManualCodeInput != nil || callbacks.OnManualCodeInputContext != nil {
		var manualCtx context.Context
		manualCtx, cancelManual = context.WithCancel(ctx)
		defer cancelManual()
		manualDone = make(chan struct{})
		manualCh := make(chan struct {
			val string
			err error
		}, 1)
		go func() {
			defer close(manualDone)
			var v string
			var e error
			if callbacks.OnManualCodeInputContext != nil {
				v, e = callbacks.OnManualCodeInputContext(manualCtx)
			} else {
				v, e = callbacks.OnManualCodeInput()
			}
			manualCh <- struct {
				val string
				err error
			}{v, e}
		}()

		select {
		case r := <-resultCh:
			if r != nil {
				code, state = r.Code, r.State
			}
		case m := <-manualCh:
			if m.err != nil {
				return OAuthCredentials{}, m.err
			}
			code, state = parseAuthorizationInput(m.val)
			if state == "" {
				state = pkce.Verifier
			}
		case <-ctx.Done():
			return OAuthCredentials{}, ctx.Err()
		}
	} else {
		select {
		case r := <-resultCh:
			if r != nil {
				code, state = r.Code, r.State
			}
		case <-ctx.Done():
			return OAuthCredentials{}, ctx.Err()
		}
	}

	// Fallback: prompt for code
	if code == "" && callbacks.OnPrompt != nil {
		input, promptErr := callbacks.OnPrompt(OAuthPrompt{
			Message:     "Paste the authorization code or full redirect URL:",
			Placeholder: anthropicRedirectURI,
		})
		if promptErr != nil {
			return OAuthCredentials{}, promptErr
		}
		code, state = parseAuthorizationInput(input)
		if state == "" {
			state = pkce.Verifier
		}
	}

	if code == "" {
		return OAuthCredentials{}, fmt.Errorf("missing authorization code")
	}
	if state != pkce.Verifier {
		return OAuthCredentials{}, fmt.Errorf("OAuth state mismatch")
	}

	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for tokens...")
	}
	// upstream: packages/ai/src/auth/oauth/anthropic.ts:loginAnthropic returns the exchange Promise without awaiting it, so finally runs before the exchange settles.
	var credential OAuthCredentials
	var exchangeErr error
	exchangeDone := make(chan struct{})
	go func() {
		defer close(exchangeDone)
		credential, exchangeErr = exchangeAnthropicCode(ctx, code, state, pkce.Verifier, redirectURI)
	}()
	finishAuthorization()
	<-exchangeDone
	return credential, exchangeErr
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

func (AnthropicOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return LoginAnthropic(ctx, callbacks)
}

func (a AnthropicOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return a.RefreshTokenContext(context.Background(), creds)
}

func (AnthropicOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	return RefreshAnthropicToken(ctx, creds.Refresh)
}
