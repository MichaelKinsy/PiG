package ai

// Mirrors upstream .upstream/current/packages/ai/src/utils/oauth/openai-codex.ts.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	codexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	codexTokenURL     = "https://auth.openai.com/oauth/token"
	codexCallbackPort = 1455
	codexCallbackPath = "/auth/callback"
	codexRedirectURI  = "http://localhost:1455/auth/callback"
	codexScope        = "openid profile email offline_access"
	codexJWTClaimPath = "https://api.openai.com/auth"

	// Codex device-code (RFC 8628) endpoints. Mirrors openai-codex.ts:36-39.
	codexDeviceUserCodeURL        = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL           = "https://auth.openai.com/api/accounts/deviceauth/token"
	codexDeviceVerificationURI    = "https://auth.openai.com/codex/device"
	codexDeviceRedirectURI        = "https://auth.openai.com/deviceauth/callback"
	codexDeviceCodeTimeoutSeconds = 15 * 60

	// OpenAICodexBrowserLoginMethod and OpenAICodexDeviceCodeLoginMethod are
	// the onSelect option ids for the two Codex login methods. Mirror
	// openai-codex.ts:41-42.
	OpenAICodexBrowserLoginMethod    = "browser"
	OpenAICodexDeviceCodeLoginMethod = "device_code"
)

// codexCallbackHost is resolved at flow time so PI_OAUTH_CALLBACK_HOST
// overrides take effect (mirrors upstream's `CALLBACK_HOST` env read).
func codexCallbackHost() string {
	if v := os.Getenv("PI_OAUTH_CALLBACK_HOST"); v != "" {
		return v
	}
	return "127.0.0.1"
}

func codexCreateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// parseCodexAuthorizationInput extracts code (and optional state) from user-pasted
// input. Mirrors upstream parseAuthorizationInput in openai-codex.ts:50-77.
func parseCodexAuthorizationInput(input string) (code, state string) {
	v := strings.TrimSpace(input)
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
		q, qerr := url.ParseQuery(v)
		if qerr == nil {
			return q.Get("code"), q.Get("state")
		}
	}
	return v, ""
}

// startCodexCallbackServer starts a local HTTP server for the OAuth callback
// on the loopback interface. Mirrors upstream startLocalOAuthServer
// (openai-codex.ts:202-279).
func startCodexCallbackServer(expectedState string) (srv *http.Server, listener net.Listener, resultCh chan *callbackResult, err error) {
	resultCh = make(chan *callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(codexCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != expectedState {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, OAuthErrorHTML("State mismatch.", ""))
			return
		}
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, OAuthErrorHTML("Missing authorization code.", ""))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, OAuthSuccessHTML("OpenAI authentication completed. You can close this window."))
		select {
		case resultCh <- &callbackResult{Code: code, State: q.Get("state")}: // upstream: ai/src/auth/oauth/openai-codex.ts:settled
		default:
		}
	})
	// Anything else is a 404 with the error page.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == codexCallbackPath {
			return // handled above
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, OAuthErrorHTML("Callback route not found.", ""))
	})

	srv = &http.Server{Handler: mux}
	listener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", codexCallbackHost(), codexCallbackPort))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listen on port %d: %w", codexCallbackPort, err)
	}
	go func() {
		_ = srv.Serve(listener) // returns on Shutdown
	}()
	return srv, listener, resultCh, nil
}

type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type TokenFailure struct {
	Type    string
	Message string
	Status  int
}

func exchangeCodexAuthorizationCode(ctx context.Context, code, verifier, redirectURI string) (OAuthCredentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}
	return postCodexTokenForm(ctx, form)
}

// RefreshCodexToken refreshes an OpenAI Codex OAuth token. Mirrors upstream
// refreshAccessToken in openai-codex.ts:133-180.
func RefreshCodexToken(ctx context.Context, refreshToken string) (OAuthCredentials, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {codexClientID},
	}
	return postCodexTokenForm(ctx, form)
}

func postCodexTokenForm(ctx context.Context, form url.Values) (OAuthCredentials, error) {
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthCredentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	var tok codexTokenResponse
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return OAuthCredentials{}, fmt.Errorf("token exchange invalid JSON: %w", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.ExpiresIn == 0 {
		return OAuthCredentials{}, fmt.Errorf("token response missing required fields")
	}
	return OAuthCredentials{
		Refresh: tok.RefreshToken,
		Access:  tok.AccessToken,
		// Mirror upstream's "now + expires_in*1000": no 5-minute safety
		// margin (upstream stores raw; refresh-when-expired is checked
		// against wall clock at access time).
		Expires: time.Now().UnixMilli() + tok.ExpiresIn*1000,
	}, nil
}

// CodexAccountID extracts the chatgpt_account_id claim from a Codex access
// token, if present. Mirrors upstream getAccountId (openai-codex.ts:281-285).
// Returns "" if the token doesn't carry the expected JWT shape.
func CodexAccountID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}
	// Base64url decode the payload.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some tokens pad the payload; try the standard variant.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	auth, ok := claims[codexJWTClaimPath].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := auth["chatgpt_account_id"].(string)
	return id
}

// --- Codex device-code (RFC 8628) flow ---
// Mirrors openai-codex.ts startOpenAICodexDeviceAuth/pollOpenAICodexDeviceAuth/
// loginOpenAICodexDeviceCode (openai-codex.ts:195-451).

type codexDeviceAuthInfo struct {
	deviceAuthID    string
	userCode        string
	intervalSeconds float64
}

type codexDeviceToken struct {
	authorizationCode string
	codeVerifier      string
}

// postCodexDeviceJSON POSTs a JSON payload to a Codex device endpoint and
// returns the status code and raw body. The request is ctx-bound so the
// parity harness can mock http.DefaultClient.
func postCodexDeviceJSON(ctx context.Context, urlStr string, payload any) (int, []byte, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

// codexBodySuffix mirrors upstream's `${body ? ": " + body : ""}`.
func codexBodySuffix(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return ": " + string(body)
}

// codexParseInterval mirrors upstream's `typeof interval === "string" ?
// Number(interval.trim()) : interval` followed by the finite/>=0 validation.
// A JSON number unmarshals to float64; a string is parsed (an empty/whitespace
// string is 0, matching JS Number("") === 0).
func codexParseInterval(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return 0, false
		}
		return t, true
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return 0, true
		}
		n, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// codexExtractErrorCode parses {error: string | {code: string}} from a Codex
// device error body. Mirrors openai-codex.ts:278-285.
func codexExtractErrorCode(body []byte) string {
	var wrap struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &wrap) != nil || len(wrap.Error) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(wrap.Error, &s) == nil {
		return s
	}
	var obj struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(wrap.Error, &obj) == nil {
		return obj.Code
	}
	return ""
}

// startCodexDeviceAuth requests a device auth id + user code from the Codex
// device endpoint. Mirrors startOpenAICodexDeviceAuth (openai-codex.ts:195-236).
func startCodexDeviceAuth(ctx context.Context) (codexDeviceAuthInfo, error) {
	status, body, err := postCodexDeviceJSON(ctx, codexDeviceUserCodeURL, map[string]string{"client_id": codexClientID})
	if err != nil {
		return codexDeviceAuthInfo{}, err
	}
	if status != http.StatusOK {
		if status == http.StatusNotFound {
			return codexDeviceAuthInfo{}, fmt.Errorf("OpenAI Codex device code login is not enabled for this server. Use browser login or verify the server URL.")
		}
		return codexDeviceAuthInfo{}, fmt.Errorf("OpenAI Codex device code request failed with status %d%s", status, codexBodySuffix(body))
	}
	var j struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     any    `json:"interval"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return codexDeviceAuthInfo{}, fmt.Errorf("Invalid OpenAI Codex device code response: %s", string(body))
	}
	interval, ok := codexParseInterval(j.Interval)
	if j.DeviceAuthID == "" || j.UserCode == "" || !ok || interval < 0 {
		return codexDeviceAuthInfo{}, fmt.Errorf("Invalid OpenAI Codex device code response: %s", string(body))
	}
	return codexDeviceAuthInfo{deviceAuthID: j.DeviceAuthID, userCode: j.UserCode, intervalSeconds: interval}, nil
}

// pollCodexDeviceAuth polls the Codex device token endpoint until the user
// approves. Mirrors pollOpenAICodexDeviceAuth (openai-codex.ts:239-300).
func pollCodexDeviceAuth(ctx context.Context, device codexDeviceAuthInfo) (codexDeviceToken, error) {
	interval := device.intervalSeconds
	expires := float64(codexDeviceCodeTimeoutSeconds)
	return PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[codexDeviceToken]{
		IntervalSeconds:  &interval,
		ExpiresInSeconds: &expires,
		Poll: func() (DeviceCodePollResult[codexDeviceToken], error) {
			status, body, err := postCodexDeviceJSON(ctx, codexDeviceTokenURL, map[string]string{
				"device_auth_id": device.deviceAuthID,
				"user_code":      device.userCode,
			})
			if err != nil {
				return DeviceCodePollResult[codexDeviceToken]{}, err
			}
			if status == http.StatusOK {
				var j struct {
					AuthorizationCode string `json:"authorization_code"`
					CodeVerifier      string `json:"code_verifier"`
				}
				if json.Unmarshal(body, &j) != nil || j.AuthorizationCode == "" || j.CodeVerifier == "" {
					return DeviceCodePollResult[codexDeviceToken]{
						Status:  DevicePollFailed,
						Message: fmt.Sprintf("Invalid OpenAI Codex device auth token response: %s", string(body)),
					}, nil
				}
				return DeviceCodePollResult[codexDeviceToken]{
					Status: DevicePollComplete,
					Value:  codexDeviceToken{authorizationCode: j.AuthorizationCode, codeVerifier: j.CodeVerifier},
				}, nil
			}
			if status == http.StatusForbidden || status == http.StatusNotFound {
				return DeviceCodePollResult[codexDeviceToken]{Status: DevicePollPending}, nil
			}
			switch codexExtractErrorCode(body) {
			case "deviceauth_authorization_pending":
				return DeviceCodePollResult[codexDeviceToken]{Status: DevicePollPending}, nil
			case "slow_down":
				return DeviceCodePollResult[codexDeviceToken]{Status: DevicePollSlowDown}, nil
			}
			return DeviceCodePollResult[codexDeviceToken]{
				Status:  DevicePollFailed,
				Message: fmt.Sprintf("OpenAI Codex device auth failed with status %d%s", status, codexBodySuffix(body)),
			}, nil
		},
	})
}

// LoginOpenAICodexDeviceCode runs the Codex device-code login: request a user
// code, surface it via onDeviceCode, poll for approval, then exchange the
// authorization code for credentials. Mirrors loginOpenAICodexDeviceCode
// (openai-codex.ts:433-451).
func LoginOpenAICodexDeviceCode(ctx context.Context, onDeviceCode func(OAuthDeviceCodeInfo)) (OAuthCredentials, error) {
	device, err := startCodexDeviceAuth(ctx)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if onDeviceCode != nil {
		onDeviceCode(OAuthDeviceCodeInfo{
			UserCode:         device.userCode,
			VerificationURI:  codexDeviceVerificationURI,
			IntervalSeconds:  device.intervalSeconds,
			ExpiresInSeconds: codexDeviceCodeTimeoutSeconds,
		})
	}
	token, err := pollCodexDeviceAuth(ctx, device)
	if err != nil {
		return OAuthCredentials{}, err
	}
	return exchangeCodexAuthorizationCode(ctx, token.authorizationCode, token.codeVerifier, codexDeviceRedirectURI)
}

// LoginOpenAICodex runs the OpenAI Codex (ChatGPT) OAuth flow.
func LoginOpenAICodex(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := codexCreateState()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate state: %w", err)
	}

	srv, _, resultCh, err := startCodexCallbackServer(state)
	// A failed bind is silent upstream: waitForCode resolves null and the
	// manual code prompt completes the login. A nil channel never delivers.
	hasServer := err == nil
	if hasServer {
		defer func() {
			shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer shutCancel()
			_ = srv.Shutdown(shutCtx)
		}()
	} else {
		resultCh = nil
	}

	originator := "pi"
	// Build the authorize URL with the EXACT parameter order upstream
	// uses (URLSearchParams preserves insertion order; Go's url.Values
	// would alphabetize and diverge). See
	// .upstream/current/packages/ai/src/utils/oauth/openai-codex.ts:193-204.
	authURL := codexAuthorizeURL +
		"?response_type=code" +
		"&client_id=" + url.QueryEscape(codexClientID) +
		"&redirect_uri=" + url.QueryEscape(codexRedirectURI) +
		"&scope=" + url.QueryEscape(codexScope) +
		"&code_challenge=" + url.QueryEscape(pkce.Challenge) +
		"&code_challenge_method=S256" +
		"&state=" + url.QueryEscape(state) +
		"&id_token_add_organizations=true" +
		"&codex_cli_simplified_flow=true" +
		"&originator=" + url.QueryEscape(originator)

	callbacks.OnAuth(OAuthAuthInfo{
		URL:          authURL,
		Instructions: "A browser window should open. Complete login to finish.",
	})

	var code string
	if callbacks.OnManualCodeInput != nil {
		// Race the local callback against manual paste, mirroring upstream
		// loginOpenAICodex, which prompts whether or not the server bound.
		manualCh := make(chan struct {
			val string
			err error
		}, 1)
		go func() {
			v, e := callbacks.OnManualCodeInput()
			manualCh <- struct {
				val string
				err error
			}{v, e}
		}()

		select {
		case r := <-resultCh:
			if r != nil {
				code = r.Code
				if r.State != state {
					return OAuthCredentials{}, fmt.Errorf("OAuth state mismatch")
				}
			}
		case m := <-manualCh:
			if m.err != nil {
				return OAuthCredentials{}, m.err
			}
			parsedCode, parsedState := parseCodexAuthorizationInput(m.val)
			if parsedState != "" && parsedState != state {
				return OAuthCredentials{}, errors.New("State mismatch")
			}
			code = parsedCode
		case <-ctx.Done():
			return OAuthCredentials{}, ctx.Err()
		}
	} else if hasServer {
		select {
		case r := <-resultCh:
			if r != nil {
				code = r.Code
				if r.State != state {
					return OAuthCredentials{}, fmt.Errorf("OAuth state mismatch")
				}
			}
		case <-ctx.Done():
			return OAuthCredentials{}, ctx.Err()
		}
	}

	// Callers without a manual code callback are prompted when the callback
	// server failed to bind.
	if !hasServer && callbacks.OnManualCodeInput == nil && callbacks.OnPrompt != nil {
		input, promptErr := callbacks.OnPrompt(OAuthPrompt{
			Message:     "Paste the authorization code or full redirect URL:",
			Placeholder: codexRedirectURI,
		})
		if promptErr != nil {
			return OAuthCredentials{}, promptErr
		}
		code, _ = parseCodexAuthorizationInput(input)
	}

	if code == "" {
		return OAuthCredentials{}, errors.New("Missing authorization code")
	}

	return exchangeCodexAuthorizationCode(ctx, code, pkce.Verifier, codexRedirectURI)
}

// OpenAICodexOAuthDisplayName is the OAuth method label upstream renders for
// ChatGPT Plus/Pro Codex subscription login.
const OpenAICodexOAuthDisplayName = "OpenAI (ChatGPT Plus/Pro)"

// CodexOAuthProvider implements OAuthProviderInterface for OpenAI Codex.
type CodexOAuthProvider struct{}

func (CodexOAuthProvider) ID() string                          { return "openai-codex" }
func (CodexOAuthProvider) IsSubscription() bool                { return true }
func (CodexOAuthProvider) Name() string                        { return OpenAICodexOAuthDisplayName }
func (CodexOAuthProvider) UsesCallbackServer() bool            { return true }
func (CodexOAuthProvider) GetAPIKey(c OAuthCredentials) string { return c.Access }

// Login presents the browser/device-code method selection, then runs the
// chosen flow. Mirrors upstream openaiCodexOAuthProvider.login
// (openai-codex.ts:568-595).
func (c CodexOAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return c.LoginContext(context.Background(), callbacks)
}

// LoginContext presents the browser/device-code method selection and carries ctx through the selected flow.
func (c CodexOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	method := OpenAICodexBrowserLoginMethod
	if callbacks.OnSelect != nil {
		selected, err := callbacks.OnSelect(OAuthSelectPrompt{
			Message: "Select OpenAI Codex login method:",
			Options: []OAuthSelectOption{
				{ID: OpenAICodexBrowserLoginMethod, Label: "Browser login (default)"},
				{ID: OpenAICodexDeviceCodeLoginMethod, Label: "Device code login (headless)"},
			},
		})
		if err != nil {
			return OAuthCredentials{}, err
		}
		if selected == "" {
			return OAuthCredentials{}, fmt.Errorf("Login cancelled")
		}
		method = selected
	}
	switch method {
	case OpenAICodexDeviceCodeLoginMethod:
		return LoginOpenAICodexDeviceCode(ctx, callbacks.OnDeviceCode)
	case OpenAICodexBrowserLoginMethod:
		return LoginOpenAICodex(ctx, callbacks)
	default:
		return OAuthCredentials{}, fmt.Errorf("Unknown OpenAI Codex login method: %s", method)
	}
}
func (c CodexOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return c.RefreshTokenContext(context.Background(), creds)
}
func (CodexOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	return RefreshCodexToken(ctx, creds.Refresh)
}
