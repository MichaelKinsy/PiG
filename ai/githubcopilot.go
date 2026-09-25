package ai

// Mirrors upstream pi-ai:
//   - utils/oauth/github-copilot.js     (device flow, refresh, model enablement)
//   - providers/github-copilot-headers.js (X-Initiator, vision flag, intent)
//   - models.generated.js                (static User-Agent / Editor-Version headers)
//
// The Copilot provider speaks the OpenAI Completions API on top of the
// per-user proxy endpoint advertised in the access token's `proxy-ep` claim.
// We implement it as a thin wrapper around openAIProvider, plumbing in:
//   - GetAPIKey:    auto-refresh when the cached access token is near expiry
//   - GetBaseURL:   parse proxy-ep from the (post-refresh) access token
//   - DynamicHeaders: X-Initiator (user/agent), Openai-Intent, optional
//     Copilot-Vision-Request when any image content is present

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// CopilotClientID is the OAuth client ID used by the upstream pi-ai package
// (also the same one VS Code's Copilot Chat extension uses). Stored
// base64-encoded in upstream; decoded at startup.
var copilotClientID = mustDecodeB64("SXYxLmI1MDdhMDhjODdlY2ZlOTg=")

// copilotStaticHeaders are sent on every request to the Copilot proxy.
// These exact values matter: the proxy filters on them.
var copilotStaticHeaders = map[string]string{
	"User-Agent":             "GitHubCopilotChat/0.35.0",
	"Editor-Version":         "vscode/1.107.0",
	"Editor-Plugin-Version":  "copilot-chat/0.35.0",
	"Copilot-Integration-Id": "vscode-chat",
}

// copilotAPIVersion is sent as X-GitHub-Api-Version on the model-catalog
// fetch. Mirrors upstream COPILOT_API_VERSION (github-copilot.ts:19).
const copilotAPIVersion = "2026-06-01"

func mustDecodeB64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(fmt.Sprintf("github-copilot: bad client id encoding: %v", err))
	}
	return string(b)
}

// ─── Device flow ──────────────────────────────────────────────────────────────

// CopilotLoginCallbacks is the user-facing surface for an interactive login.
// All callbacks are optional except OnAuth (we have to surface the user code
// somehow) and OnPrompt (only called if the caller wants to support
// enterprise domain entry: pass a no-op returning "" for vanilla github.com).
type CopilotLoginCallbacks struct {
	// OnPrompt is invoked once at the start to ask for an enterprise domain.
	// Return "" to use github.com.
	OnPrompt func(ctx context.Context) (string, error)
	// OnAuth is invoked once with the verification URL and user code that
	// the user must visit and enter.
	OnAuth func(verificationURL, userCode string)
	// OnProgress is invoked with status messages (e.g. "Enabling models...").
	OnProgress func(msg string)
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type accessTokenResponse struct {
	AccessToken           string `json:"access_token,omitempty"`
	RefreshToken          string `json:"refresh_token,omitempty"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in,omitempty"` // seconds
	Error                 string `json:"error,omitempty"`
	ErrorDescription      string `json:"error_description,omitempty"`
	Interval              int    `json:"interval,omitempty"`
}

type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // seconds since epoch
}

// LoginGitHubCopilot runs the OAuth device code flow against github.com (or
// an enterprise domain) and returns refreshed Copilot credentials. The
// returned Credential has Type=CredentialOAuth and is ready to be persisted
// via AuthStorage.Set("github-copilot", cred).
func LoginGitHubCopilot(ctx context.Context, cb CopilotLoginCallbacks) (Credential, error) {
	domain := "github.com"
	enterprise := ""
	if cb.OnPrompt != nil {
		input, err := cb.OnPrompt(ctx)
		if err != nil {
			return Credential{}, fmt.Errorf("github-copilot: prompt: %w", err)
		}
		input = strings.TrimSpace(input)
		if input != "" {
			d, ok := normalizeDomain(input)
			if !ok {
				return Credential{}, fmt.Errorf("github-copilot: invalid enterprise domain %q", input)
			}
			domain = d
			enterprise = d
		}
	}

	device, err := startDeviceFlow(ctx, domain)
	if err != nil {
		return Credential{}, fmt.Errorf("github-copilot: device flow: %w", err)
	}
	if cb.OnAuth != nil {
		cb.OnAuth(device.VerificationURI, device.UserCode)
	}

	githubAccessToken, err := pollForGitHubAccessToken(ctx, domain, device)
	if err != nil {
		return Credential{}, fmt.Errorf("github-copilot: poll: %w", err)
	}

	cred, err := refreshCopilotToken(ctx, githubAccessToken, enterprise)
	if err != nil {
		return Credential{}, fmt.Errorf("github-copilot: exchange: %w", err)
	}

	// Unlike the individual policy POSTs below, the catalog fetch itself is
	// not best-effort: upstream loginGitHubCopilot awaits it unguarded, so a
	// failure here fails the whole login. Mirrors github-copilot.ts:462-470.
	catalog, err := fetchGitHubCopilotModels(ctx, cred.Access, enterprise, copilotRetryPolicy{MaxRetries: 2, MaxElapsedMs: 5000})
	if err != nil {
		return Credential{}, fmt.Errorf("github-copilot: list models: %w", err)
	}

	// Only models whose account policy is "unconfigured" need a policy POST
	// before they can be used; enabled/disabled/absent models are left
	// alone. Mirrors upstream loginGitHubCopilot (github-copilot.ts:471-480),
	// which prints "Enabling models..." only when that list is non-empty.
	if len(catalog.PolicyModelIDs) > 0 {
		if cb.OnProgress != nil {
			cb.OnProgress("Enabling models...")
		}
		// Best-effort: enableGitHubCopilotModels swallows ordinary POST
		// failures per model and only propagates a caller cancellation.
		if _, err := enableGitHubCopilotModels(ctx, cred.Access, catalog.PolicyModelIDs, enterprise); err != nil {
			return Credential{}, fmt.Errorf("github-copilot: enable models: %w", err)
		}
	}

	return cred, nil
}

func normalizeDomain(input string) (string, bool) {
	t := strings.TrimSpace(input)
	if t == "" {
		return "", false
	}
	if !strings.Contains(t, "://") {
		t = "https://" + t
	}
	u, err := url.Parse(t)
	if err != nil || u.Host == "" {
		return "", false
	}
	return u.Host, true
}

func startDeviceFlow(ctx context.Context, domain string) (*deviceCodeResponse, error) {
	form := url.Values{
		"client_id": {copilotClientID},
		"scope":     {"read:user"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://%s/login/device/code", domain),
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", copilotStaticHeaders["User-Agent"])

	body, err := doJSON(req)
	if err != nil {
		return nil, err
	}
	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("decode device code: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, errors.New("invalid device code response")
	}
	return &dc, nil
}

// pollForGitHubAccessToken polls the device flow until the user authorizes.
// Returns the GitHub user-to-server access token (ghu_).
// Mirrors upstream pollForGitHubAccessToken (github-copilot.ts:170-235).
func pollForGitHubAccessToken(ctx context.Context, domain string, dc *deviceCodeResponse) (string, error) {
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	intervalMs := max(dc.Interval*1000, 1000)
	multiplier := 1.2
	const slowDownMultiplier = 1.4

	tokenURL := fmt.Sprintf("https://%s/login/oauth/access_token", domain)

	for time.Now().Before(deadline) {
		wait := time.Duration(float64(intervalMs)*multiplier) * time.Millisecond
		if remaining := time.Until(deadline); wait > remaining {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}

		form := url.Values{
			"client_id":   {copilotClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
			strings.NewReader(form.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", copilotStaticHeaders["User-Agent"])

		body, err := doJSON(req)
		if err != nil {
			return "", err
		}
		var resp accessTokenResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", fmt.Errorf("decode access token: %w", err)
		}
		if resp.AccessToken != "" {
			return resp.AccessToken, nil
		}
		switch resp.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			if resp.Interval > 0 {
				intervalMs = resp.Interval * 1000
			} else {
				intervalMs += 5000
			}
			multiplier = slowDownMultiplier
			continue
		case "":
			// no token, no error: just retry
			continue
		default:
			suffix := ""
			if resp.ErrorDescription != "" {
				suffix = ": " + resp.ErrorDescription
			}
			return "", fmt.Errorf("device flow failed: %s%s", resp.Error, suffix)
		}
	}
	return "", errors.New("device flow timed out")
}

// refreshCopilotToken exchanges a GitHub access token (the one from device
// flow, stored as `refresh` in auth.json) for a short-lived Copilot API
// token (stored as `access`).
func refreshCopilotToken(ctx context.Context, githubToken, enterpriseDomain string) (Credential, error) {
	domain := "github.com"
	if enterpriseDomain != "" {
		domain = enterpriseDomain
	}
	tokenURL := fmt.Sprintf("https://api.%s/copilot_internal/v2/token", domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return Credential{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+githubToken)
	for k, v := range copilotStaticHeaders {
		req.Header.Set(k, v)
	}

	body, err := doJSON(req)
	if err != nil {
		return Credential{}, err
	}
	var resp copilotTokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Credential{}, fmt.Errorf("decode copilot token: %w", err)
	}
	if resp.Token == "" || resp.ExpiresAt == 0 {
		return Credential{}, errors.New("invalid copilot token response")
	}
	// Mirror upstream: subtract 5min from expires for safety margin, store as ms.
	return Credential{
		Type:             CredentialOAuth,
		Refresh:          githubToken,
		Access:           resp.Token,
		Expires:          resp.ExpiresAt*1000 - 5*60*1000,
		EnterpriseDomain: enterpriseDomain,
	}, nil
}

// ─── Model catalog and policy enablement ───────────────────────────────────────

// copilotModelCatalog is the result of filtering the raw GitHub Copilot
// model-catalog response into the ids the picker should offer and the ids
// whose account policy still needs a policy-enable POST. Mirrors upstream
// parseGitHubCopilotModelCatalog's return shape (github-copilot.ts:93-133).
type copilotModelCatalog struct {
	AvailableModelIDs []string
	PolicyModelIDs    []string
}

// copilotAccountModel is one entry of the account's model catalog after
// dropping models the account can never use (no tool-call support).
type copilotAccountModel struct {
	id            string
	pickerEnabled bool
	policyState   string // "" when the API omitted policy/state entirely
}

// parseGitHubCopilotModelCatalog decodes the GET {base}/models response body
// and derives the picker-visible model ids plus the ids that need a policy
// POST before they can be used. allowPolicyFallback mirrors upstream: some
// Individual accounts report model_picker_enabled=false for every model
// despite having explicit enabled policies, so fetchGitHubCopilotModels only
// sets this true for the default individual.githubcopilot.com endpoint.
// Mirrors upstream parseGitHubCopilotModelCatalog (github-copilot.ts:93-133).
func parseGitHubCopilotModelCatalog(body []byte, allowPolicyFallback bool) (copilotModelCatalog, error) {
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		return copilotModelCatalog{}, errors.New("github-copilot: invalid models response")
	}
	rawData, ok := top["data"].([]any)
	if !ok {
		return copilotModelCatalog{}, errors.New("github-copilot: invalid models response")
	}

	accountModels := make([]copilotAccountModel, 0, len(rawData))
	for _, rawItem := range rawData {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		id, ok := item["id"].(string)
		if !ok {
			continue
		}
		if copilotToolCallsDisabled(item) {
			continue
		}
		pickerEnabled, _ := item["model_picker_enabled"].(bool)
		accountModels = append(accountModels, copilotAccountModel{
			id:            id,
			pickerEnabled: pickerEnabled,
			policyState:   copilotPolicyState(item),
		})
	}

	pickerModelIDs := make([]string, 0, len(accountModels))
	for _, m := range accountModels {
		if m.pickerEnabled && m.policyState != "disabled" {
			pickerModelIDs = append(pickerModelIDs, m.id)
		}
	}
	usePolicyFallback := allowPolicyFallback && len(pickerModelIDs) == 0

	availableModelIDs := pickerModelIDs
	if len(pickerModelIDs) == 0 && allowPolicyFallback {
		availableModelIDs = make([]string, 0, len(accountModels))
		for _, m := range accountModels {
			if m.policyState == "enabled" {
				availableModelIDs = append(availableModelIDs, m.id)
			}
		}
	}

	policyModelIDs := make([]string, 0, len(accountModels))
	for _, m := range accountModels {
		if m.policyState != "unconfigured" {
			continue
		}
		if _, known := LookupModelExact("github-copilot/" + m.id); !known {
			continue
		}
		if m.pickerEnabled || usePolicyFallback {
			policyModelIDs = append(policyModelIDs, m.id)
		}
	}

	return copilotModelCatalog{AvailableModelIDs: availableModelIDs, PolicyModelIDs: policyModelIDs}, nil
}

// copilotToolCallsDisabled reports whether the catalog item explicitly
// disables tool calls (capabilities.supports.tool_calls === false). Absent or
// non-boolean values do not disable the model. Mirrors github-copilot.ts:106.
func copilotToolCallsDisabled(item map[string]any) bool {
	caps, ok := item["capabilities"].(map[string]any)
	if !ok {
		return false
	}
	supports, ok := caps["supports"].(map[string]any)
	if !ok {
		return false
	}
	toolCalls, ok := supports["tool_calls"].(bool)
	return ok && !toolCalls
}

// copilotPolicyState returns item.policy.state, or "" when either key is
// absent or not a string. Mirrors upstream's `asRecord(item.policy)?.state`.
func copilotPolicyState(item map[string]any) string {
	policy, ok := item["policy"].(map[string]any)
	if !ok {
		return ""
	}
	state, _ := policy["state"].(string)
	return state
}

// copilotRetryPolicy mirrors upstream's inline { maxRetries, maxElapsedMs }
// retry budget (github-copilot.ts:139, 359-362, 398, 467-469).
type copilotRetryPolicy struct {
	MaxRetries   int
	MaxElapsedMs int
}

// copilotHTTPResult is one fully-drained HTTP response. fetchWithRateLimitRetry
// inspects status and headers before deciding whether to retry, so callers
// work with a decoded result rather than a live, streaming *http.Response.
type copilotHTTPResult struct {
	StatusCode int
	Status     string
	Header     http.Header
	Body       []byte
}

// copilotFetchWithRetry mirrors upstream fetchWithRateLimitRetry
// (github-copilot.ts:135-166): retries a 429 with exponential backoff (or the
// server's Retry-After), each attempt bounded by a 5s timeout, the whole
// retry budget bounded by policy.MaxElapsedMs when policy.MaxRetries > 0.
// newReq must build a fresh, unsent request for each attempt.
func copilotFetchWithRetry(ctx context.Context, policy copilotRetryPolicy, newReq func(context.Context) (*http.Request, error)) (copilotHTTPResult, error) {
	var deadline time.Time
	requestCtx := ctx
	hasBudget := policy.MaxRetries > 0 && policy.MaxElapsedMs > 0
	if hasBudget {
		deadline = time.Now().Add(time.Duration(policy.MaxElapsedMs) * time.Millisecond)
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	for retry := 0; ; retry++ {
		attemptCtx, cancel := context.WithTimeout(requestCtx, 5*time.Second)
		req, err := newReq(attemptCtx)
		if err != nil {
			cancel()
			return copilotHTTPResult{}, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			return copilotHTTPResult{}, err
		}
		delayMs, retryable := copilotRetryDelay(resp.Header.Get("Retry-After"), retry)
		retryable = retryable && resp.StatusCode == http.StatusTooManyRequests && retry != policy.MaxRetries
		if hasBudget && delayMs >= float64(deadline.UnixMilli()-time.Now().UnixMilli()) {
			retryable = false
		}
		if !retryable {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			cancel()
			if readErr != nil {
				return copilotHTTPResult{}, readErr
			}
			return copilotHTTPResult{StatusCode: resp.StatusCode, Status: resp.Status, Header: resp.Header, Body: body}, nil
		}
		// A retry discards its response body; waiting for EOF can consume the
		// entire operation budget on a rate-limit response that never finishes.
		closeErr := resp.Body.Close()
		cancel()
		if closeErr != nil {
			return copilotHTTPResult{}, closeErr
		}
		// Node setTimeout truncates fractions and uses 1ms outside this range.
		// Compare the original floating-point delay to the budget before converting.
		if delayMs < 1 || delayMs > math.MaxInt32 {
			delayMs = 1
		}
		timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
		select {
		case <-requestCtx.Done():
			timer.Stop()
			return copilotHTTPResult{}, requestCtx.Err()
		case <-timer.C:
		}
	}
}

// copilotRetryDelay returns floating-point milliseconds so the caller can
// compare finite server delays against its budget before timer conversion.
// Mirrors github-copilot.ts:154-160, including Number.parseFloat prefixes.
func copilotRetryDelay(retryAfter string, retry int) (float64, bool) {
	delayMs := 500 * math.Pow(2, float64(retry))
	if retryAfter != "" {
		if seconds := copilotParseFloat(retryAfter); !math.IsNaN(seconds) {
			delayMs = seconds * 1000
		} else if t, err := http.ParseTime(retryAfter); err == nil {
			delayMs = float64(t.UnixMilli() - time.Now().UnixMilli())
		} else {
			return 0, false
		}
	}
	if math.IsNaN(delayMs) || math.IsInf(delayMs, 0) {
		return 0, false
	}
	return math.Max(0, delayMs), true
}

var copilotFloatPrefix = regexp.MustCompile(`^[+-]?(?:Infinity|(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)

func copilotParseFloat(value string) float64 {
	value = strings.TrimLeftFunc(value, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })
	prefix := copilotFloatPrefix.FindString(value)
	if prefix == "" {
		return math.NaN()
	}
	// A range error carries the Infinity value required by Number.parseFloat.
	parsed, _ := strconv.ParseFloat(prefix, 64)
	return parsed
}

// fetchGitHubCopilotModels fetches and filters the account's model catalog.
// Mirrors upstream fetchGitHubCopilotModels (github-copilot.ts:168-195).
func fetchGitHubCopilotModels(ctx context.Context, copilotToken, enterpriseDomain string, policy copilotRetryPolicy) (copilotModelCatalog, error) {
	base := getCopilotBaseURL(copilotToken, enterpriseDomain)
	// Some Individual accounts return false for every picker flag despite
	// explicit enabled policies. Limit the fallback to that endpoint so
	// other account types keep strict picker semantics.
	allowPolicyFallback := base == "https://api.individual.githubcopilot.com"
	result, err := copilotFetchWithRetry(ctx, policy, func(reqCtx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+copilotToken)
		for k, v := range copilotStaticHeaders {
			req.Header.Set(k, v)
		}
		req.Header.Set("X-GitHub-Api-Version", copilotAPIVersion)
		return req, nil
	})
	if err != nil {
		return copilotModelCatalog{}, err
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return copilotModelCatalog{}, fmt.Errorf("%s: %s", result.Status, strings.TrimSpace(string(result.Body)))
	}
	return parseGitHubCopilotModelCatalog(result.Body, allowPolicyFallback)
}

// enableGitHubCopilotModel POSTs a policy update for one model. stopBatch
// reports whether the caller's batch loop should stop trying further models:
// true after a rate limit exhausts its retries (matching upstream's thrown
// 429 error, caught by the batch loop and turned into a break) or after
// ctx cancellation (in which case err also carries ctx.Err() so the caller
// re-raises it instead of silently stopping). Ordinary network failures
// return (false, false, nil): best-effort, the batch continues. Mirrors
// upstream enableGitHubCopilotModel (github-copilot.ts:373-408).
func enableGitHubCopilotModel(ctx context.Context, token, modelID, enterpriseDomain string) (enabled, stopBatch bool, err error) {
	base := getCopilotBaseURL(token, enterpriseDomain)
	url := fmt.Sprintf("%s/models/%s/policy", base, modelID)
	body := []byte(`{"state":"enabled"}`)
	result, fetchErr := copilotFetchWithRetry(ctx, copilotRetryPolicy{MaxRetries: 2, MaxElapsedMs: 5000}, func(reqCtx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		for k, v := range copilotStaticHeaders {
			req.Header.Set(k, v)
		}
		req.Header.Set("openai-intent", "chat-policy")
		req.Header.Set("x-interaction-type", "chat-policy")
		return req, nil
	})
	if fetchErr != nil {
		if ctx.Err() != nil {
			return false, true, ctx.Err()
		}
		return false, false, nil
	}
	if result.StatusCode == http.StatusTooManyRequests {
		return false, true, nil
	}
	return result.StatusCode >= 200 && result.StatusCode < 300, false, nil
}

// enableGitHubCopilotModels enables each requested model in order and
// returns the ids that succeeded. It stops early (without error) once a
// model's policy POST exhausts its rate-limit retries, and propagates ctx
// cancellation as an error. Mirrors upstream enableGitHubCopilotModels
// (github-copilot.ts:414-432).
func enableGitHubCopilotModels(ctx context.Context, token string, modelIDs []string, enterpriseDomain string) ([]string, error) {
	enabled := make([]string, 0, len(modelIDs))
	for _, id := range modelIDs {
		ok, stop, err := enableGitHubCopilotModel(ctx, token, id, enterpriseDomain)
		if err != nil {
			return enabled, err
		}
		if ok {
			enabled = append(enabled, id)
		}
		if stop {
			break
		}
	}
	return enabled, nil
}

// ─── Provider ─────────────────────────────────────────────────────────────────

// CopilotProviderConfig configures a github-copilot provider.
type CopilotProviderConfig struct {
	// Auth is the credential store; the provider will read and refresh
	// the github-copilot entry as needed.
	Auth *AuthStorage
	// Model is the Copilot model id (e.g. "gpt-4o", "claude-sonnet-4").
	Model string
	// API selects the wire protocol. When set to APIOpenAIResponses (or
	// "openai-responses"), the provider uses the Responses API instead of
	// the Chat Completions API. This matters for models like gpt-5.4 that
	// require the Responses endpoint when tools + reasoning are combined.
	// Mirrors upstream: the model's `api` field in models.generated.ts
	// determines which endpoint to hit: "openai-completions" vs
	// "openai-responses".
	API API
	// Reasoning indicates the selected Copilot model supports reasoning.
	// Required for the Responses API to include reasoning.effort/summary and
	// stream thinking deltas.
	Reasoning bool
	// EnvToken is the env-resolved API key (COPILOT_GITHUB_TOKEN, then
	// GH_TOKEN/GITHUB_TOKEN). When auth.json has no github-copilot OAuth
	// credential, it is used directly as the bearer, mirroring upstream
	// auth-storage.getApiKey step 4 (the env fallback returns getEnvApiKey
	// verbatim). The base URL is derived from the token's proxy-ep claim.
	EnvToken string
	// RuntimeToken returns a non-persistent runtime key (--api-key). When it
	// reports one, it is the bearer ahead of the stored credential and
	// EnvToken, as upstream's RuntimeCredentials overlay masks auth.json.
	RuntimeToken func() (string, bool)
}

// NewCopilotProvider builds a Provider that streams via the Copilot proxy.
// The wire protocol (Completions vs Responses API) is selected by cfg.API.
// The credential is auto-refreshed transparently when within 60s of expiry.
func NewCopilotProvider(cfg CopilotProviderConfig) (Provider, error) {
	if cfg.Auth == nil {
		return nil, errors.New("github-copilot: missing AuthStorage")
	}
	if cfg.Model == "" {
		return nil, errors.New("github-copilot: missing model")
	}
	mgr := &copilotTokenManager{auth: cfg.Auth, envToken: cfg.EnvToken, runtimeToken: cfg.RuntimeToken}

	dynamicHeaders := func(transcript TranscriptContext, _ StreamOptions) map[string]string {
		messages := transcript.Messages()
		h := map[string]string{
			"X-Initiator":   inferInitiator(messages),
			"Openai-Intent": "conversation-edits",
		}
		if hasImages(messages) {
			h["Copilot-Vision-Request"] = "true"
		}
		return h
	}

	// Route to the Anthropic Messages API for Claude models proxied through
	// Copilot. Upstream dispatches by model.api ("anthropic-messages") and the
	// Anthropic provider has a github-copilot branch that uses Bearer auth
	// instead of x-api-key, plus Copilot dynamic headers.
	if cfg.API == APIAnthropicMessages {
		// Copilot's Anthropic proxy does not support all native Anthropic
		// API features (e.g. eager_input_streaming on tool definitions).
		// Disable features that the proxy rejects.
		eagerStreaming := false
		inner := NewAnthropicProvider(AnthropicConfig{
			Model:          cfg.Model,
			ProviderID:     "github-copilot",
			ExtraHeaders:   copilotStaticHeaders,
			GetAPIKey:      mgr.getAccessToken,
			GetBaseURL:     mgr.getBaseURL,
			DynamicHeaders: dynamicHeaders,
			UseBearerAuth:  true,
			Compat: &AnthropicMessagesCompat{
				SupportsEagerToolInputStreaming: &eagerStreaming,
			},
		})
		return inner, nil
	}

	// Route to the Responses API when the model requires it.
	// Upstream models.generated.ts tags e.g. github-copilot/gpt-5.4 with
	// api: "openai-responses"; the Completions endpoint rejects the
	// reasoning_effort + tools combination for these models.
	if cfg.API == APIOpenAIResponses {
		isReasoning := cfg.Reasoning
		if !isReasoning {
			if generated, ok := LookupModel("github-copilot/" + cfg.Model); ok {
				isReasoning = generated.Reasoning
			}
		}
		inner := NewOpenAIResponsesProvider(OpenAIResponsesConfig{
			Model:          cfg.Model,
			ProviderID:     "github-copilot",
			ExtraHeaders:   copilotStaticHeaders,
			IsReasoning:    isReasoning,
			GetAPIKey:      mgr.getAccessToken,
			GetBaseURL:     mgr.getBaseURL,
			DynamicHeaders: dynamicHeaders,
		})
		return inner, nil
	}

	// Default: Chat Completions API.
	inner := NewOpenAIProvider(OpenAIConfig{
		Model:          cfg.Model,
		ProviderID:     "github-copilot",
		ExtraHeaders:   copilotStaticHeaders,
		GetAPIKey:      mgr.getAccessToken,
		GetBaseURL:     mgr.getBaseURL,
		DynamicHeaders: dynamicHeaders,
	})
	return inner, nil
}

// copilotTokenManager handles read/refresh of the github-copilot credential.
type copilotTokenManager struct {
	auth         *AuthStorage
	envToken     string
	runtimeToken func() (string, bool)
	mu           sync.Mutex
}

// runtimeKey returns the runtime key, if one is set.
func (m *copilotTokenManager) runtimeKey() (string, bool) {
	if m.runtimeToken == nil {
		return "", false
	}
	key, ok := m.runtimeToken()
	return key, ok && key != ""
}

func (m *copilotTokenManager) loadCred() (Credential, error) {
	cred, ok, err := m.auth.Get("github-copilot")
	if err != nil {
		return Credential{}, err
	}
	if !ok {
		return Credential{}, errors.New("github-copilot: not logged in (run `pi login github-copilot`)")
	}
	if cred.Refresh == "" {
		return Credential{}, errors.New("github-copilot: missing refresh token; re-login required")
	}
	return cred, nil
}

// getAccessToken returns the current Copilot access token, refreshing if
// it is missing or expires within 60 seconds.
//
// Two-level refresh:
//  1. Copilot token (~30 min TTL): call refreshCopilotToken with GitHub token.
//  2. GitHub token (~8 h TTL, expiring apps): if refreshCopilotToken fails
//     AND we have a GitHubRefreshToken, renew the GitHub token first via
//     renewGitHubToken, then retry refreshCopilotToken. Persists the new
//     GitHub credential so subsequent calls don't repeat the renewal.
func (m *copilotTokenManager) getAccessToken(ctx context.Context) (string, error) {
	if key, ok := m.runtimeKey(); ok {
		return key, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cred, err := m.loadCred()
	if err != nil {
		// No usable stored OAuth credential. Mirror upstream
		// auth-storage.getApiKey priority: fall back to the env API key
		// (COPILOT_GITHUB_TOKEN/GH_TOKEN/GITHUB_TOKEN) and use it directly as
		// the bearer. Used as-is, not refreshed, matching upstream which
		// returns getEnvApiKey() verbatim from the env fallback.
		if m.envToken != "" {
			return m.envToken, nil
		}
		return "", err
	}
	now := time.Now().UnixMilli()
	if cred.Access != "" && cred.Expires > now+60_000 {
		return cred.Access, nil
	}
	// Copilot API token expired: refresh using the stored GitHub access
	// token (ghu_). Mirrors upstream auth-storage.ts getApiKey →
	// provider.refreshToken → refreshGitHubCopilotToken.
	fresh, err := refreshCopilotToken(ctx, cred.Refresh, cred.EnterpriseDomain)
	if err != nil {
		// If the context was canceled (user abort), propagate the
		// cancellation directly so callers can distinguish user abort
		// from real auth failure. Without this, "context canceled"
		// gets wrapped as "refresh failed" and triggers false
		// "run pig login" auth guidance.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// Upstream does not classify this failure: refreshGitHubCopilotToken
		// simply throws, the credential resolves to undefined, and the session
		// reports a hedged message naming both causes. Mirror that wording
		// rather than asserting expiry: a refresh also fails on rate limits,
		// network loss, and provider outages, none of which a login fixes.
		return "", fmt.Errorf("github-copilot: token refresh failed: credentials may have expired or the network is unavailable: %w", err)
	}
	if err := m.auth.Set("github-copilot", fresh); err != nil {
		return "", fmt.Errorf("github-copilot: persist refreshed token: %w", err)
	}
	return fresh.Access, nil
}

// getBaseURL extracts the per-user proxy endpoint from the (refreshed)
// access token.
func (m *copilotTokenManager) getBaseURL(ctx context.Context) (string, error) {
	// Resolve the bearer first (refreshes a stored credential when needed, or
	// returns the env token). The base URL is derived from that token's
	// proxy-ep claim, mirroring upstream getGitHubCopilotBaseUrl(token).
	// A runtime key (--api-key) is API-key auth: upstream keeps the model's
	// base URL and interprets only an OAuth token's proxy-ep claim.
	if _, runtime := m.runtimeKey(); runtime {
		return getCopilotBaseURL("", ""), nil
	}
	tok, err := m.getAccessToken(ctx)
	if err != nil {
		return "", err
	}
	domain := ""
	if cred, e := m.loadCred(); e == nil {
		domain = cred.EnterpriseDomain
	}
	return getCopilotBaseURL(tok, domain), nil
}

// getCopilotBaseURL parses the `proxy-ep=...` claim from the access token
// and replaces a leading proxy. prefix with api., preserving all other hosts.
// Falls back to enterprise / default if the token is unparseable.
func getCopilotBaseURL(accessToken, enterpriseDomain string) string {
	if accessToken != "" {
		for part := range strings.SplitSeq(accessToken, ";") {
			if v, ok := strings.CutPrefix(part, "proxy-ep="); ok {
				v = strings.TrimSpace(v)
				if v != "" {
					if host, ok := strings.CutPrefix(v, "proxy."); ok {
						v = "api." + host
					}
					return "https://" + v
				}
			}
		}
	}
	if enterpriseDomain != "" {
		return "https://copilot-api." + enterpriseDomain
	}
	return "https://api.individual.githubcopilot.com"
}

// ResolveCopilotEnvFallback resolves the github-copilot bearer and base URL via
// the same copilotTokenManager NewCopilotProvider uses, for an auth store with
// no stored credential and the given env API key. It lets the parity harness
// assert the env-fallback path (upstream auth-storage.getApiKey step 4) without
// a live network; production resolution runs through the identical manager
// inside the provider's stream path.
func ResolveCopilotEnvFallback(auth *AuthStorage, envToken string) (bearer, baseURL string, err error) {
	mgr := &copilotTokenManager{auth: auth, envToken: envToken}
	ctx := context.Background()
	if bearer, err = mgr.getAccessToken(ctx); err != nil {
		return "", "", err
	}
	if baseURL, err = mgr.getBaseURL(ctx); err != nil {
		return "", "", err
	}
	return bearer, baseURL, nil
}

// ─── Per-request dynamic headers ──────────────────────────────────────────────

// inferInitiator returns "agent" if the most recent message is from the
// assistant or a tool result, "user" otherwise. Mirrors upstream
// inferCopilotInitiator().
func inferInitiator(msgs []Message) string {
	if len(msgs) == 0 {
		return "user"
	}
	switch msgs[len(msgs)-1].(type) {
	case UserMessage:
		return "user"
	default:
		return "agent"
	}
}

// hasImages reports whether any message carries image content, including
// images returned by tools (e.g. the read tool reading a PNG). Triggers the
// Copilot-Vision-Request header. Mirrors upstream hasCopilotVisionInput, which
// checks both user messages and tool-result messages for image blocks.
func hasImages(messages []Message) bool {
	for _, message := range messages {
		switch message := message.(type) {
		case UserMessage:
			blocks, ok := message.Content.(UserContentBlocks)
			if !ok {
				continue
			}
			for _, block := range blocks {
				if _, ok := block.(ImageContent); ok {
					return true
				}
			}
		case ToolResultMessage:
			for _, block := range message.Content {
				if _, ok := block.(ImageContent); ok {
					return true
				}
			}
		}
	}
	return false
}

// ─── small helpers ────────────────────────────────────────────────────────────

func doJSON(req *http.Request) ([]byte, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
