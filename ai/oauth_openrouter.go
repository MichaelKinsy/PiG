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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
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
func openRouterCallbackHost() string {
	if h := strings.TrimSpace(os.Getenv("PI_OAUTH_CALLBACK_HOST")); h != "" {
		return h
	}
	return "127.0.0.1"
}

// parseOpenRouterAuthorizationInput extracts the authorization code from a
// pasted redirect URL, a `code=`-bearing query fragment, or a bare code.
// Mirrors upstream parseAuthorizationInput.
func parseOpenRouterAuthorizationInput(input string) string {
	v := strings.TrimSpace(input)
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

// openRouterResult carries the outcome of the callback race: a claimed callback
// exchange (cred), a failure (err), or a hand-off to manual paste (both nil).
type openRouterResult struct {
	cred *OAuthCredentials
	err  error
}

// openRouterCallback is the one-shot loopback callback server plus the
// claimed/settled state machine that lets a claimed browser callback win over a
// racing manual paste. Mirrors upstream OpenRouterCallbackServer.
type openRouterCallback struct {
	callbackURL string
	resultCh    chan openRouterResult
	srv         *http.Server

	mu      sync.Mutex
	claimed bool
	settled bool

	done      chan struct{}
	closeOnce sync.Once
}

// finish delivers the first (and only) settled result. Never closes the server
// itself: the login function's deferred close() does that after the result is
// read, so a callback handler is never blocked draining its own server.
func (c *openRouterCallback) finish(r openRouterResult) {
	c.mu.Lock()
	if c.settled {
		c.mu.Unlock()
		return
	}
	c.settled = true
	c.mu.Unlock()
	c.resultCh <- r
}

// cancelWait hands the login over to manual paste, unless a callback already
// claimed the exchange (in which case that exchange settles the login).
func (c *openRouterCallback) cancelWait() {
	c.mu.Lock()
	claimed := c.claimed
	c.mu.Unlock()
	if !claimed {
		c.finish(openRouterResult{})
	}
}

func (c *openRouterCallback) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.srv.Shutdown(shutCtx)
	})
}

func sendOpenRouterHTML(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}

// startOpenRouterCallbackServer binds an ephemeral loopback port and serves the
// single-use callback route.
func startOpenRouterCallbackServer(ctx context.Context, callbackPath, verifier string) (*openRouterCallback, error) {
	if ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}
	host := openRouterCallbackHost()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, fmt.Errorf("bind OpenRouter OAuth callback: %w", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, errors.New("could not determine the OpenRouter OAuth callback port")
	}

	c := &openRouterCallback{
		callbackURL: fmt.Sprintf("http://%s:%d%s", host, addr.Port, callbackPath),
		resultCh:    make(chan openRouterResult, 1),
		done:        make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != callbackPath {
			sendOpenRouterHTML(w, http.StatusNotFound, OAuthErrorHTML("OAuth callback route not found.", ""))
			return
		}
		c.mu.Lock()
		if c.claimed || c.settled {
			c.mu.Unlock()
			sendOpenRouterHTML(w, http.StatusConflict, OAuthErrorHTML("This OAuth callback has already been used.", ""))
			return
		}
		q := r.URL.Query()
		if oauthErr := q.Get("error"); oauthErr != "" {
			c.mu.Unlock()
			desc := q.Get("error_description")
			if desc == "" {
				desc = oauthErr
			}
			sendOpenRouterHTML(w, http.StatusBadRequest, OAuthErrorHTML("OpenRouter authorization was denied.", desc))
			c.finish(openRouterResult{err: fmt.Errorf("OpenRouter authorization failed: %s", desc)})
			return
		}
		code := q.Get("code")
		if code == "" {
			c.mu.Unlock()
			sendOpenRouterHTML(w, http.StatusBadRequest, OAuthErrorHTML("OpenRouter returned no authorization code.", ""))
			return
		}
		c.claimed = true
		c.mu.Unlock()

		cred, err := exchangeOpenRouterCode(ctx, code, verifier)
		if err != nil {
			sendOpenRouterHTML(w, http.StatusBadGateway, OAuthErrorHTML("OpenRouter key exchange failed.", err.Error()))
			c.finish(openRouterResult{err: err})
			return
		}
		sendOpenRouterHTML(w, http.StatusOK, OAuthSuccessHTML("Signed in to OpenRouter. You may now close this page."))
		c.finish(openRouterResult{cred: &cred})
	})

	c.srv = &http.Server{Handler: mux}
	go func() { _ = c.srv.Serve(listener) }()

	// Owned watcher: the login timeout and cancellation settle the race through
	// resultCh rather than a detached goroutine. close() stops it via `done`.
	go func() {
		timer := time.NewTimer(openRouterLoginTimeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			c.finish(openRouterResult{err: errors.New("Login cancelled")})
		case <-timer.C:
			c.finish(openRouterResult{err: errors.New("OpenRouter OAuth login timed out")})
		case <-c.done:
		}
	}()

	return c, nil
}

func openRouterCallbackPath() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "/oauth/callback/" + hex.EncodeToString(b), nil
}

// LoginOpenRouter runs the OpenRouter OAuth PKCE flow: a loopback callback
// raced against a manual paste. A claimed browser callback always wins over a
// racing paste. Mirrors upstream loginOpenRouter.
func LoginOpenRouter(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}
	callbackPath, err := openRouterCallbackPath()
	if err != nil {
		return OAuthCredentials{}, err
	}
	cb, err := startOpenRouterCallbackServer(ctx, callbackPath, pkce.Verifier)
	if err != nil {
		return OAuthCredentials{}, err
	}
	defer cb.close()

	params := url.Values{
		"callback_url":          {cb.callbackURL},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
	}
	authURL := openRouterAuthorizeURL + "?" + params.Encode()

	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Listening for OpenRouter OAuth callback on " + cb.callbackURL)
	}
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{
			URL:          authURL,
			Instructions: "Complete sign-in in your browser. If the browser is on another machine, paste the final redirect URL here.",
		})
	}

	// The manual paste races the callback. When it resolves it hands the login
	// over via cancelWait, which is a no-op once a callback has claimed.
	manualCh := make(chan struct {
		val string
		err error
	}, 1)
	if callbacks.OnManualCodeInput != nil {
		go func() {
			v, e := callbacks.OnManualCodeInput()
			manualCh <- struct {
				val string
				err error
			}{v, e}
			cb.cancelWait()
		}()
	}

	res := <-cb.resultCh
	if res.err != nil {
		return OAuthCredentials{}, res.err
	}
	if res.cred != nil {
		return *res.cred, nil
	}

	// The callback was unclaimed and manual paste won the race.
	m := <-manualCh
	if m.err != nil {
		return OAuthCredentials{}, m.err
	}
	code := parseOpenRouterAuthorizationInput(m.val)
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
