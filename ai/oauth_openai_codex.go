package ai

// Ports packages/ai/src/auth/oauth/openai-codex.ts.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
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

func codexCreateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// parseCodexAuthorizationInput extracts code (and optional state) from user-pasted
// input. Mirrors upstream parseAuthorizationInput in openai-codex.ts:69-97.
func parseCodexAuthorizationInput(input string) (code, state string) {
	v := trimJSWhitespace(input)
	if v == "" {
		return "", ""
	}
	// Try as URL
	if query, ok := authorizationURLQuery(v); ok {
		return query.Get("code"), query.Get("state")
	}
	// code#state
	if code, state, ok := authorizationFragmentPair(v); ok {
		return code, state
	}
	// code=X&state=Y
	if strings.Contains(v, "code=") {
		query := authorizationQueryParams(v)
		return query.Get("code"), query.Get("state")
	}
	return v, ""
}

// codexTokenResponse holds the members readTokenResponse reads (openai-codex.ts:127-146). Each member stays raw: Pi tests truthiness and typeof on the parsed value, so a mistyped member is a missing field, not a decode error.
type codexTokenResponse struct {
	AccessToken  json.RawMessage `json:"access_token"`
	RefreshToken json.RawMessage `json:"refresh_token"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
}

// nonEmptyJSONString returns the value of a JSON string member that is a JavaScript truthy string. Pi also accepts any other truthy value and would store it as the token; a token that is not a string cannot be a Credential and reports the missing-fields error.
func nonEmptyJSONString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" {
		return "", false
	}
	return value, true
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
	operation := "exchange"
	if form.Get("grant_type") == "refresh_token" {
		operation = "refresh"
	}
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
		if operation == "refresh" {
			return OAuthCredentials{}, fmt.Errorf("OpenAI Codex token refresh error: %w", err)
		}
		if ctx.Err() != nil {
			return OAuthCredentials{}, errors.New("Login cancelled")
		}
		return OAuthCredentials{}, fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := string(respBody)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return OAuthCredentials{}, fmt.Errorf("OpenAI Codex token %s failed (%d): %s", operation, resp.StatusCode, message)
	}
	var parsed json.RawMessage
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return OAuthCredentials{}, fmt.Errorf("token exchange invalid JSON: %w", err)
	}
	// `json?.access_token` reads a member of an object only; every other JSON value has none.
	var tok codexTokenResponse
	if trimmed := bytes.TrimSpace(parsed); len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(parsed, &tok); err != nil {
			return OAuthCredentials{}, fmt.Errorf("token exchange invalid JSON: %w", err)
		}
	}
	access, accessOK := nonEmptyJSONString(tok.AccessToken)
	refresh, refreshOK := nonEmptyJSONString(tok.RefreshToken)
	if !accessOK || !refreshOK || !jsonNumberToken(tok.ExpiresIn) {
		stringified, err := jsonstringify.Canonicalize(parsed)
		if err != nil {
			return OAuthCredentials{}, fmt.Errorf("token exchange invalid JSON: %w", err)
		}
		return OAuthCredentials{}, fmt.Errorf("OpenAI Codex token %s response missing fields: %s", operation, stringified)
	}
	creds := OAuthCredentials{Refresh: refresh, Access: access}
	// Mirror upstream's "Date.now() + expires_in * 1000": no safety margin, a fractional value stays exact and an overflowing literal is Infinity.
	creds.SetExpiresMillis(float64(nowMillis()) + jsnumber.FromJSON(tok.ExpiresIn)*1000)
	return creds, nil
}

// jsAtob is `atob(value)`: the forgiving-base64 decode of the WHATWG infra standard. It strips ASCII whitespace, drops one or two trailing "=" when
// the length is a multiple of 4, and reads each decoded byte as one character U+0000 to U+00FF. The unpadded standard decoder rejects the rest: a
// length of 1 mod 4, any character outside the standard alphabet ("-" and "_" included) and any other "=".
func jsAtob(value string) (string, bool) {
	value = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\f' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, value)
	if len(value)%4 == 0 {
		value = strings.TrimSuffix(strings.TrimSuffix(value, "="), "=")
	}
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return string(runes), true
}

// CodexAccountID is Pi's getAccountId (openai-codex.ts:310): the chatgpt_account_id claim of the access token, "" when the token lacks the JWT shape
// or the claim. Pi's decodeJwt reads the payload with atob and JSON.parse, so the claim is read as Latin-1 characters of the decoded bytes.
func CodexAccountID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, ok := jsAtob(parts[1])
	if !ok {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(payload), &claims); err != nil {
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
		trimmed := trimJSWhitespace(t)
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

// LoginOpenAICodex runs the OpenAI Codex (ChatGPT) OAuth flow. Port 1455 is shared with the Codex CLI; when it is taken,
// the pasted redirect URL completes the login.
// Ports loginOpenAICodex (.upstream/v0.99.1/packages/ai/src/auth/oauth/openai-codex.ts:359).
func LoginOpenAICodex(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := codexCreateState()
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("generate state: %w", err)
	}
	callback, err := StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[string]{
		ProviderName: "OpenAI",
		Host:         oauthCallbackHost(),
		Port:         codexCallbackPort,
		Path:         codexCallbackPath,
		State:        &state,
		Complete:     func(_ context.Context, code string) (string, error) { return code, nil },
	})
	if err != nil {
		callback = nil
	} else {
		defer callback.Close()
	}

	// pig divergence (D26): PiG names itself as the OpenAI login's originator. An app-supplied agentName replaces the default, as createAuthorizationFlow(options?.agentName) does (openai-codex.ts:363); an empty name is kept.
	originator := pigidentity.CodexOriginator
	if callbacks.AgentName != nil {
		originator = *callbacks.AgentName
	}
	// Build the authorize URL with the exact parameter order upstream uses: URLSearchParams preserves insertion order and
	// url.Values would alphabetize (openai-codex.ts:295-309).
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
	callbacks.OnAuth(OAuthAuthInfo{URL: authURL, Instructions: "A browser window should open. Complete login to finish."})

	result, err := WaitForCallbackOrManualInput(ctx, manualCodeInteraction(callbacks), callback, AuthManualCodePrompt{
		Message:     "Complete login in your browser, or paste the authorization code / redirect URL here:",
		Placeholder: codexRedirectURI,
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	var code string
	if result.Callback {
		code = result.Value
	} else {
		parsedCode, parsedState := parseCodexAuthorizationInput(result.Input)
		if parsedState != "" && parsedState != state {
			return OAuthCredentials{}, errors.New("State mismatch")
		}
		code = parsedCode
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
		return codexCredentialsFromToken(LoginOpenAICodexDeviceCode(ctx, callbacks.OnDeviceCode))
	case OpenAICodexBrowserLoginMethod:
		return codexCredentialsFromToken(LoginOpenAICodex(ctx, callbacks))
	default:
		return OAuthCredentials{}, fmt.Errorf("Unknown OpenAI Codex login method: %s", method)
	}
}
func (c CodexOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return c.RefreshTokenContext(context.Background(), creds)
}
func (CodexOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	return codexCredentialsFromToken(RefreshCodexToken(ctx, creds.Refresh))
}

func codexCredentialsFromToken(credentials OAuthCredentials, err error) (OAuthCredentials, error) {
	if err != nil {
		return OAuthCredentials{}, err
	}
	accountID := CodexAccountID(credentials.Access)
	if accountID == "" {
		return OAuthCredentials{}, errors.New("Failed to extract accountId from token")
	}
	credentials.AccountID = accountID
	return credentials, nil
}
