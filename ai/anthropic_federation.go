package ai

// Ports the workload identity federation of packages/ai/src/api/anthropic-messages.ts (getAnthropicFederation, the
// federation client of createClient) together with the parts of @anthropic-ai/sdk@0.124.0 it relies on:
// lib/credentials/oidc-federation.mjs (the jwt-bearer token exchange), types.mjs (endpoint check, response parsing,
// redaction), identity-token.mjs (the identity token file) and token-cache.mjs (proactive refresh).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

const (
	anthropicOAuthAPIBeta       = "oauth-2025-04-20"
	anthropicFederationBeta     = "oidc-federation-2026-04-01"
	anthropicJWTBearerGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	anthropicTokenEndpoint      = "/v1/oauth/token"
	anthropicSDKUserAgent       = "Anthropic/JS 0.124.0"
	// upstream: node_modules/@anthropic-ai/sdk/lib/credentials/oidc-federation.mjs:jwt
	anthropicMaxIdentityTokenChars = 16 * 1024
	// upstream: node_modules/@anthropic-ai/sdk/lib/credentials/types.mjs:MAX_TOKEN_RESPONSE_BYTES
	anthropicMaxTokenResponseBytes = 1 << 20
	// upstream: node_modules/@anthropic-ai/sdk/lib/credentials/types.mjs:MAX_ERROR_BODY_CHARS
	anthropicMaxErrorBodyChars      = 2000
	anthropicAdvisoryRefreshSeconds = 120
	anthropicMandatoryRefreshSecond = 30
	// upstream: node_modules/@anthropic-ai/sdk/lib/credentials/types.mjs:ADVISORY_REFRESH_BACKOFF_IN_SECONDS
	anthropicAdvisoryBackoffSeconds = 5
)

// anthropicFederation is the workload identity federation config built from the ANTHROPIC_* variables.
// upstream: packages/ai/src/api/anthropic-messages.ts:351-372 (getAnthropicFederation)
type anthropicFederation struct {
	OrganizationID    string `json:"organization_id"`
	WorkspaceID       string `json:"workspace_id,omitempty"`
	FederationRuleID  string `json:"federation_rule_id"`
	ServiceAccountID  string `json:"service_account_id,omitempty"`
	IdentityTokenFile string `json:"identity_token_file"`
}

// anthropicHasRequestAuth reports whether the request already carries credentials.
// upstream: packages/ai/src/api/anthropic-messages.ts:318-325 (hasRequestAuth)
func anthropicHasRequestAuth(apiKey string, headers anthropicHeaders) bool {
	if apiKey != "" {
		return true
	}
	for _, entry := range headers.entries {
		if !entry.defined || entry.value == nil || trimJSWhitespace(*entry.value) == "" {
			continue
		}
		for _, name := range []string{"authorization", "x-api-key", "cf-aig-authorization"} {
			if strings.EqualFold(entry.name, name) {
				return true
			}
		}
	}
	return false
}

// getAnthropicFederation returns the federation config for the anthropic provider when no key or auth header was resolved and the rule, organization and identity token file are set.
// upstream: packages/ai/src/api/anthropic-messages.ts:351-372 (getAnthropicFederation)
func getAnthropicFederation(providerID, apiKey string, headers anthropicHeaders, env ProviderEnv) *anthropicFederation {
	if providerID != "anthropic" || anthropicHasRequestAuth(apiKey, headers) {
		return nil
	}
	variables := AnthropicFederationEnv(env)
	if variables == nil {
		return nil
	}
	return &anthropicFederation{
		OrganizationID:    variables[AnthropicOrganizationIDEnv],
		WorkspaceID:       variables[AnthropicWorkspaceIDEnv],
		FederationRuleID:  variables[AnthropicFederationRuleIDEnv],
		ServiceAccountID:  variables[AnthropicServiceAccountIDEnv],
		IdentityTokenFile: variables[AnthropicIdentityTokenFileEnv],
	}
}

// AnthropicFederationAuthSource is the source the anthropic auth resolver reports when workload identity federation is the only credential.
// upstream: packages/ai/src/providers/anthropic.ts:68
const AnthropicFederationAuthSource = "workload identity federation"

// AnthropicFederationEnv returns the workload identity federation variables set in env or the process env, or nil unless the rule, organization and identity token file are all set. The service account and workspace travel when set.
// The anthropic auth resolver, the federation client and the coding registry's availability checks share it, so they agree on when federation is configured.
// upstream: packages/ai/src/providers/anthropic.ts:47-69, packages/ai/src/api/anthropic-messages.ts:343-372
func AnthropicFederationEnv(env ProviderEnv) ProviderEnv {
	return anthropicFederationEnvFrom(func(name string) string { return getProviderEnvValue(name, env) })
}

// anthropicFederationEnvFrom reads the three required variables in order, then the two optional ones; an unset or empty value is absent.
func anthropicFederationEnvFrom(lookup func(name string) string) ProviderEnv {
	federation := ProviderEnv{}
	for _, name := range []string{AnthropicFederationRuleIDEnv, AnthropicOrganizationIDEnv, AnthropicIdentityTokenFileEnv} {
		value := lookup(name)
		if value == "" {
			return nil
		}
		federation[name] = value
	}
	for _, name := range []string{AnthropicServiceAccountIDEnv, AnthropicWorkspaceIDEnv} {
		if value := lookup(name); value != "" {
			federation[name] = value
		}
	}
	return federation
}

// fetchRejectionText is String(rejection) for the rejection Node's fetch raises for err: undici rejects with TypeError "fetch failed" or "terminated", and the signal's reason is a DOMException named AbortError. A caller-supplied fetch's own error is an Error.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/lib/credentials/oidc-federation.mjs:47-49 (`Failed to reach token endpoint ${url}: ${err}`)
func fetchRejectionText(err error) string {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		err = urlErr.Err
	}
	if transport, ok := errors.AsType[*nodeTransportError](err); ok {
		switch transport.message {
		case "fetch failed", "terminated":
			return "TypeError: " + transport.message
		case "This operation was aborted":
			return "AbortError: " + transport.message
		}
	}
	return "Error: " + err.Error()
}

// anthropicAccessToken is a minted access token. ExpiresAt is Unix seconds, fractional as the SDK's nowAsSeconds() + expires_in; nil means no expiry.
type anthropicAccessToken struct {
	Token     string
	ExpiresAt *float64
}

// anthropicTokenProvider mints an access token; forceRefresh follows an invalidation.
type anthropicTokenProvider func(ctx context.Context, forceRefresh bool) (anthropicAccessToken, error)

// anthropicFederationNow is the clock of the token cache.
var anthropicFederationNow = time.Now

func anthropicNowSeconds() int64 { return anthropicFederationNow().Unix() }

// anthropicTokenRefresh is one in-flight provider call that concurrent callers share.
type anthropicTokenRefresh struct {
	done  chan struct{}
	token anthropicAccessToken
	err   error
}

// anthropicTokenCache wraps a token provider with two-tier proactive refresh and concurrent deduplication.
// Exchanges run in cache-owned tasks so that a caller that stops waiting does not abort the exchange other callers
// share; Close cancels and drains them.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/lib/credentials/token-cache.mjs:22-107 (TokenCache)
type anthropicTokenCache struct {
	provider        anthropicTokenProvider
	onAdvisoryError func(error)
	ctx             context.Context
	cancel          context.CancelFunc
	tasks           sync.WaitGroup

	mu                sync.Mutex
	cached            *anthropicAccessToken
	pending           *anthropicTokenRefresh
	nextForce         bool
	lastAdvisoryError int64
	closed            bool
}

func newAnthropicTokenCache(provider anthropicTokenProvider, onAdvisoryError func(error)) *anthropicTokenCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &anthropicTokenCache{provider: provider, onAdvisoryError: onAdvisoryError, ctx: ctx, cancel: cancel}
}

// GetToken returns a token to authenticate a request.
func (cache *anthropicTokenCache) GetToken(ctx context.Context) (string, error) {
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	cache.mu.Lock()
	force := cache.nextForce
	cache.nextForce = false
	cached := cache.cached
	switch {
	case force || cached == nil:
		refresh := cache.refreshLocked(force)
		cache.mu.Unlock()
		return refresh.wait(ctx)
	case cached.ExpiresAt == nil:
	default:
		remaining := *cached.ExpiresAt - float64(anthropicNowSeconds())
		switch {
		case remaining > anthropicAdvisoryRefreshSeconds:
		case remaining > anthropicMandatoryRefreshSecond:
			cache.backgroundRefreshLocked()
		default:
			refresh := cache.refreshLocked(false)
			cache.mu.Unlock()
			return refresh.wait(ctx)
		}
	}
	token := cached.Token
	cache.mu.Unlock()
	return token, nil
}

// Invalidate clears the cached token and forces the next refresh: a 401 says the token is bad whatever its expiry.
func (cache *anthropicTokenCache) Invalidate() {
	cache.mu.Lock()
	cache.cached = nil
	cache.nextForce = true
	cache.mu.Unlock()
}

// Close cancels the in-flight exchanges and waits for them to end.
func (cache *anthropicTokenCache) Close() {
	cache.mu.Lock()
	cache.closed = true
	cache.mu.Unlock()
	cache.cancel()
	cache.tasks.Wait()
}

// refreshLocked is the mandatory refresh. It joins an in-flight refresh unless forced: a forced refresh must not
// coalesce into a non-forced one that may re-serve the same stale token.
func (cache *anthropicTokenCache) refreshLocked(force bool) *anthropicTokenRefresh {
	if cache.pending != nil && !force {
		return cache.pending
	}
	return cache.startLocked(force, false)
}

// backgroundRefreshLocked is the advisory refresh. It shares the in-flight refresh, reports a failure through the hook,
// and backs off after one so an outage in the advisory window does not hammer the token endpoint.
func (cache *anthropicTokenCache) backgroundRefreshLocked() {
	if cache.pending != nil || anthropicNowSeconds()-cache.lastAdvisoryError < anthropicAdvisoryBackoffSeconds {
		return
	}
	cache.startLocked(false, true)
}

func (cache *anthropicTokenCache) startLocked(force, advisory bool) *anthropicTokenRefresh {
	refresh := &anthropicTokenRefresh{done: make(chan struct{})}
	if cache.closed {
		refresh.err = errors.New("Anthropic federation token cache is closed")
		close(refresh.done)
		return refresh
	}
	cache.pending = refresh
	cache.tasks.Go(func() {
		token, err := cache.provider(cache.ctx, force)
		cache.mu.Lock()
		if err == nil {
			cache.cached = &token
		} else if advisory {
			cache.lastAdvisoryError = anthropicNowSeconds()
		}
		cache.pending = nil
		cache.mu.Unlock()
		refresh.token, refresh.err = token, err
		close(refresh.done)
		if err != nil && advisory && cache.onAdvisoryError != nil {
			cache.onAdvisoryError(err)
		}
	})
	return refresh
}

// wait blocks until the refresh ends or the caller's context does.
func (refresh *anthropicTokenRefresh) wait(ctx context.Context) (string, error) {
	select {
	case <-refresh.done:
		if refresh.err != nil {
			return "", refresh.err
		}
		return refresh.token.Token, nil
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}

// anthropicFederationClient is the client kept for the current federation config and fetch. Requests share its token cache.
// upstream: packages/ai/src/api/anthropic-messages.ts:373-379 (federationClient)
type anthropicFederationClient struct {
	key     string
	fetch   *http.Client
	tokens  *anthropicTokenCache
	users   int
	retired bool
}

var anthropicFederationClients struct {
	sync.Mutex
	current *anthropicFederationClient
}

// anthropicFederationLease is one request's hold on the shared federation client. A replaced client closes when its last lease is released.
type anthropicFederationLease struct {
	client *anthropicFederationClient
	once   sync.Once
}

// acquireAnthropicFederationClient keeps one client for the current base URL, federation config and fetch; a different one builds a client with its own token cache.
// upstream: packages/ai/src/api/anthropic-messages.ts:1054-1067
func acquireAnthropicFederationClient(baseURL string, federation anthropicFederation, fetch *http.Client) *anthropicFederationLease {
	raw, _ := json.Marshal([]any{baseURL, federation})
	key := string(raw)
	var closing *anthropicFederationClient
	anthropicFederationClients.Lock()
	client := anthropicFederationClients.current
	if client == nil || client.key != key || client.fetch != fetch {
		if client != nil {
			client.retired = true
			if client.users == 0 {
				closing = client
			}
		}
		exchange := providerHTTPClient(streamingHTTPClientNoRetry(), fetch)
		client = &anthropicFederationClient{key: key, fetch: fetch, tokens: newAnthropicTokenCache(func(ctx context.Context, _ bool) (anthropicAccessToken, error) {
			return exchangeAnthropicFederationToken(ctx, exchange, baseURL, federation)
		}, nil)}
		anthropicFederationClients.current = client
	}
	client.users++
	anthropicFederationClients.Unlock()
	if closing != nil {
		closing.tokens.Close()
	}
	return &anthropicFederationLease{client: client}
}

func (lease *anthropicFederationLease) release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		var closing *anthropicFederationClient
		anthropicFederationClients.Lock()
		lease.client.users--
		if lease.client.retired && lease.client.users == 0 {
			closing = lease.client
		}
		anthropicFederationClients.Unlock()
		if closing != nil {
			closing.tokens.Close()
		}
	})
}

// authorize sets the federated bearer token on a request about to be sent.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/client.mjs:345-358 (authHeaders), 429-447 (prepareRequest)
func (lease *anthropicFederationLease) authorize(ctx context.Context, request *http.Request) error {
	token, err := lease.client.tokens.GetToken(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("Request aborted")
		}
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	existing := request.Header.Get("anthropic-beta")
	for feature := range strings.SplitSeq(existing, ",") {
		if trimJSWhitespace(feature) == anthropicOAuthAPIBeta {
			return nil
		}
	}
	if existing == "" {
		request.Header.Set("anthropic-beta", anthropicOAuthAPIBeta)
	} else {
		request.Header.Set("anthropic-beta", existing+", "+anthropicOAuthAPIBeta)
	}
	return nil
}

// invalidateAfter401 drops the cached token when a request that used it was rejected.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/client.mjs:705-719 (shouldRetry)
func (lease *anthropicFederationLease) invalidateAfter401(status int) {
	if lease != nil && status == http.StatusUnauthorized {
		lease.client.tokens.Invalidate()
	}
}

// exchangeAnthropicFederationToken exchanges the identity token for an access token with the RFC 7523 jwt-bearer grant.
// Each call reads the identity token file and performs a fresh exchange.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/lib/credentials/oidc-federation.mjs:15-66
func exchangeAnthropicFederationToken(ctx context.Context, client *http.Client, baseURL string, federation anthropicFederation) (anthropicAccessToken, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if err := requireSecureAnthropicTokenEndpoint(baseURL); err != nil {
		return anthropicAccessToken{}, err
	}
	jwt, err := readAnthropicIdentityToken(federation.IdentityTokenFile)
	if err != nil {
		return anthropicAccessToken{}, err
	}
	if length := utf16Length(jwt); length > anthropicMaxIdentityTokenChars {
		return anthropicAccessToken{}, fmt.Errorf("Identity token is %d KiB, exceeds the 16 KiB assertion limit", (length+1023)/1024)
	}
	body, err := json.Marshal(struct {
		GrantType        string `json:"grant_type"`
		Assertion        string `json:"assertion"`
		FederationRuleID string `json:"federation_rule_id"`
		OrganizationID   string `json:"organization_id"`
		ServiceAccountID string `json:"service_account_id,omitempty"`
		WorkspaceID      string `json:"workspace_id,omitempty"`
	}{anthropicJWTBearerGrantType, jwt, federation.FederationRuleID, federation.OrganizationID, federation.ServiceAccountID, federation.WorkspaceID})
	if err != nil {
		return anthropicAccessToken{}, err
	}
	endpoint := baseURL + anthropicTokenEndpoint
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return anthropicAccessToken{}, fmt.Errorf("Failed to reach token endpoint %s: %w", endpoint, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-beta", anthropicOAuthAPIBeta+","+anthropicFederationBeta)
	request.Header.Set("User-Agent", anthropicSDKUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return anthropicAccessToken{}, fmt.Errorf("Failed to reach token endpoint %s: %s", endpoint, fetchRejectionText(err))
	}
	defer func() { _ = response.Body.Close() }()
	requestID := response.Header.Get("Request-Id")
	reply := readAnthropicTokenBody(response.Body)
	text := reply.text
	if response.StatusCode < 200 || response.StatusCode > 299 {
		hint := ""
		if response.StatusCode == http.StatusUnauthorized {
			middle := ""
			if federation.WorkspaceID == "" {
				middle = "If your federation rule is scoped to multiple workspaces, set the ANTHROPIC_WORKSPACE_ID environment variable, the 'workspace_id' config key, or the `workspaceId` option. "
			}
			hint = " Ensure your federation rule matches your identity token. " + middle + "View your authentication events in the Workload identity page of Claude Console for more details."
		}
		requestIDText := ""
		if requestID != "" {
			requestIDText = " (request-id " + requestID + ")"
		}
		redacted := redactAnthropicTokenBody(string(text))
		if reply.truncated && anthropicBodyCannotBeJSON(text) {
			// resp.text() is read in full, so the count of cut characters covers the whole body (types.mjs:66-101).
			redacted = truncateAnthropicTokenText(string(text), reply.units)
		}
		return anthropicAccessToken{}, fmt.Errorf("Token exchange failed with status %d%s: %s%s", response.StatusCode, requestIDText, redacted, hint)
	}
	return parseAnthropicTokenResponse(response.StatusCode, string(text))
}

// anthropicTokenBody is a token endpoint response text. text holds at most anthropicMaxTokenResponseBytes; units is the UTF-16 length of the whole decoded body, which counts the bytes past that limit without holding them.
type anthropicTokenBody struct {
	text      []byte
	units     int
	truncated bool
}

// readAnthropicTokenBody reads a response body for the exchange. The SDK reads it in full with resp.text() (oidc-federation.mjs); Go keeps a bounded prefix and counts the rest, a read error ends the text where it occurred, as before.
func readAnthropicTokenBody(r io.Reader) anthropicTokenBody {
	var body anthropicTokenBody
	var carry []byte
	total := 0
	chunk := make([]byte, 32*1024)
	count := func(data []byte, final bool) []byte {
		for len(data) > 0 && (final || utf8.FullRune(data)) {
			r, width := utf8.DecodeRune(data)
			body.units += utf16Units(r)
			data = data[width:]
		}
		return data
	}
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			read := chunk[:n]
			total += n
			if room := anthropicMaxTokenResponseBytes - len(body.text); room > 0 {
				body.text = append(body.text, read[:min(room, len(read))]...)
			}
			carry = count(append(carry, read...), false)
		}
		if err != nil {
			count(carry, true)
			break
		}
	}
	body.truncated = total > anthropicMaxTokenResponseBytes
	return body
}

// anthropicBodyCannotBeJSON reports whether a prefix of a longer body already rules out JSON for the whole body: a syntax error, or a complete value followed by anything but whitespace. A prefix that ends inside a valid value says nothing about the rest.
func anthropicBodyCannotBeJSON(prefix []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(prefix))
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF)
	}
	return len(bytes.TrimLeft(prefix[decoder.InputOffset():], " \t\r\n")) > 0
}

// requireSecureAnthropicTokenEndpoint rejects a base URL that would send the assertion over cleartext HTTP; loopback hosts are allowed.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/lib/credentials/types.mjs:23-41
func requireSecureAnthropicTokenEndpoint(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || (parsed.Host == "" && parsed.Opaque == "") {
		return fmt.Errorf("Invalid token endpoint base URL %q: TypeError: Invalid URL", baseURL)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if host := strings.ToLower(parsed.Hostname()); parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return fmt.Errorf("Refusing to send credential over non-https token endpoint %q", baseURL)
}

// readAnthropicIdentityToken reads the JWT from its file on every exchange, so a rotated token is picked up.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/lib/credentials/identity-token.mjs:5-25
func readAnthropicIdentityToken(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		// Node reports the read failure as "Error: <CODE>: <description>, <syscall> ['<path>']".
		return "", fmt.Errorf("Failed to read identity token file at %s: Error: %w", path, nodeerrno.FromPathError(err))
	}
	token := trimJSWhitespace(strings.ToValidUTF8(string(content), "\uFFFD"))
	if token == "" {
		return "", fmt.Errorf("Identity token file at %s is empty", path)
	}
	return token, nil
}

// parseAnthropicTokenResponse validates a token endpoint response.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/lib/credentials/types.mjs:49-65 (parseTokenResponse), oidc-federation.mjs:59-66
func parseAnthropicTokenResponse(status int, text string) (anthropicAccessToken, error) {
	raw := json.RawMessage(text)
	if !json.Valid(raw) {
		return anthropicAccessToken{}, fmt.Errorf("Token endpoint returned non-JSON response (status %d)", status)
	}
	// A body that is not an object has no members; every lookup is undefined.
	members := map[string]json.RawMessage{}
	if object, ok := jsonObjectOf(raw); ok {
		members = object
	}
	if !jsonValueTruthy(members["access_token"]) {
		return anthropicAccessToken{}, fmt.Errorf("Token endpoint response missing access_token: %s", redactAnthropicTokenJSON(raw))
	}
	if tokenType := members["token_type"]; jsonValueTruthy(tokenType) {
		if name, ok := jsonStringOf(tokenType); !ok || strings.ToLower(name) != "bearer" {
			return anthropicAccessToken{}, fmt.Errorf("Token endpoint response: unsupported token_type \"%s\" (want Bearer)", jsonAsJSString(tokenType))
		}
	}
	expiresIn := jsNumberFromJSON(members["expires_in"])
	if math.IsNaN(expiresIn) || math.IsInf(expiresIn, 0) {
		return anthropicAccessToken{}, fmt.Errorf("Token endpoint response missing required fields: %s", redactAnthropicTokenJSON(raw))
	}
	expiresAt := float64(anthropicNowSeconds()) + expiresIn
	return anthropicAccessToken{Token: jsonAsJSString(members["access_token"]), ExpiresAt: &expiresAt}, nil
}

// redactAnthropicTokenBody ports redactSensitive for a response text: JSON keeps only the RFC 6749 §5.2 error fields
// and plain text is truncated.
// upstream: node_modules/@anthropic-ai/sdk@0.124.0/lib/credentials/types.mjs:66-101
func redactAnthropicTokenBody(body string) string {
	if json.Valid([]byte(body)) {
		return redactAnthropicTokenJSON(json.RawMessage(body))
	}
	return truncateAnthropicTokenText(body, utf16Length(body))
}

// truncateAnthropicTokenText keeps the first anthropicMaxErrorBodyChars UTF-16 units of a plain-text body whose full length is units.
func truncateAnthropicTokenText(body string, units int) string {
	if units <= anthropicMaxErrorBodyChars {
		return body
	}
	head := utf16.Encode([]rune(body))
	return string(utf16.Decode(head[:anthropicMaxErrorBodyChars])) + "... <" + strconv.Itoa(units-anthropicMaxErrorBodyChars) + " more chars>"
}

// redactAnthropicTokenJSON is JSON.stringify(redactSensitive(JSON.parse(raw))): an object keeps its error fields in source order, a string is redacted as a body, and every other value becomes null.
func redactAnthropicTokenJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "null"
	}
	switch trimmed[0] {
	case '{':
		entries, err := orderedJSONObject(trimmed)
		if err != nil {
			return "null"
		}
		var out strings.Builder
		out.WriteByte('{')
		for _, entry := range entries {
			if entry.key != "error" && entry.key != "error_description" && entry.key != "error_uri" {
				continue
			}
			if out.Len() > 1 {
				out.WriteByte(',')
			}
			out.WriteString(jsonStringJS(entry.key) + ":" + stringifyJSONValue(entry.value))
		}
		out.WriteByte('}')
		return out.String()
	case '"':
		body, _ := jsonStringOf(trimmed)
		return jsonStringJS(redactAnthropicTokenBody(body))
	default:
		return "null"
	}
}

// stringifyJSONValue re-encodes a JSON value as JSON.stringify would: no whitespace and strings without HTML escaping.
func stringifyJSONValue(raw json.RawMessage) string {
	if text, ok := jsonStringOf(raw); ok {
		return jsonStringJS(text)
	}
	var out bytes.Buffer
	if json.Compact(&out, raw) != nil {
		return "null"
	}
	return out.String()
}

// jsonAsJSString is String(value) for a parsed JSON string or number, and the compact JSON text for other values.
func jsonAsJSString(raw json.RawMessage) string {
	if text, ok := jsonStringOf(raw); ok {
		return text
	}
	return stringifyJSONValue(raw)
}

// jsNumberFromJSON is Number(value) for the JSON value of a token response member; an absent member is NaN.
func jsNumberFromJSON(raw json.RawMessage) float64 {
	switch trimmed := string(bytes.TrimSpace(raw)); trimmed {
	case "":
		return math.NaN()
	case "null", "false":
		return 0
	case "true":
		return 1
	}
	if number, ok := finiteNumberOf(raw); ok {
		return number
	}
	if text, ok := jsonStringOf(raw); ok {
		text = trimJSWhitespace(text)
		if text == "" {
			return 0
		}
		if number, err := strconv.ParseFloat(text, 64); err == nil {
			return number
		}
	}
	return math.NaN()
}
