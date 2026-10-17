package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

const (
	kimiOAuthClientID        = "17e5f671-d194-4dfb-9706-5516cb48c098" // gitleaks:allow -- public OAuth client ID published by upstream Pi
	kimiDefaultOAuthHost     = "https://auth.kimi.com"
	kimiDeviceTimeoutSeconds = 15 * 60
	kimiDefaultPollSeconds   = 5
	kimiRequestTimeout       = 30 * time.Second
	kimiRefreshMaxRetries    = 3
)

type kimiOAuthProvider struct {
	client    *http.Client
	oauthHost string
	sleep     func(context.Context, time.Duration) error
}

// kimiDeviceAuthorization is a validated device-authorization response, its interval and lifetime defaulted.
type kimiDeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Interval                float64
	ExpiresIn               float64
}

// kimiTokenResponse is a token response whose fields parseKimiToken has validated.
type kimiTokenResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    float64
}

// kimiBody is what upstream's readJson returns (kimi-coding.ts:49-55) with the text JSON.stringify gives it: an object or array the body parsed to, or absent (null) when the body is not JSON or not an object. Array bodies have no members. A leading byte order mark is ignored, as Response.json() does.
type kimiBody struct {
	members map[string]any
	// text is JSON.stringify(json): the canonical form of the body, or "null".
	text string
}

func readKimiJSON(raw []byte) kimiBody {
	raw = []byte(fetchBodyText(raw))
	if !json.Valid(raw) {
		return kimiBody{text: "null"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return kimiBody{text: "null"}
	}
	var members map[string]any
	switch typed := parsed.(type) {
	case map[string]any:
		members = typed
	case []any:
	default:
		return kimiBody{text: "null"}
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return kimiBody{members: members, text: "null"}
	}
	return kimiBody{members: members, text: string(canonical)}
}

func (body kimiBody) str(key string) (string, bool) {
	value, ok := body.members[key].(string)
	return value, ok
}

// num is a JSON number's JavaScript value: past the double range it is infinite, as JSON.parse reads it.
func (body kimiBody) num(key string) (float64, bool) {
	value, ok := body.members[key].(json.Number)
	if !ok {
		return 0, false
	}
	parsed, _ := strconv.ParseFloat(string(value), 64)
	return parsed, true
}

// description is upstream's `: ${error_description}` when the member is a string, empty ones included.
func (body kimiBody) description() string {
	if value, ok := body.str("error_description"); ok {
		return ": " + value
	}
	return ""
}

func newKimiOAuthProvider() kimiOAuthProvider {
	host := os.Getenv("KIMI_CODE_OAUTH_HOST")
	if host == "" {
		host = os.Getenv("KIMI_OAUTH_HOST")
	}
	if host == "" {
		host = kimiDefaultOAuthHost
	}
	return kimiOAuthProvider{
		client:    &http.Client{},
		oauthHost: strings.TrimRight(host, "/"),
	}
}

func (kimiOAuthProvider) ID() string               { return "kimi-coding" }
func (kimiOAuthProvider) IsSubscription() bool     { return true }
func (kimiOAuthProvider) Name() string             { return "Kimi For Coding" }
func (kimiOAuthProvider) UsesCallbackServer() bool { return false }
func (p kimiOAuthProvider) GetAPIKey(creds OAuthCredentials) string {
	return creds.Access
}

// Login runs the flow without cancellation. Prefer LoginContext.
func (p kimiOAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.LoginContext(context.Background(), callbacks)
}

// LoginContext runs the device flow with the owning operation's cancellation.
func (p kimiOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	device, err := p.startDeviceAuthorization(ctx)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnDeviceCode != nil {
		callbacks.OnDeviceCode(OAuthDeviceCodeInfo{
			UserCode:         device.UserCode,
			VerificationURI:  device.VerificationURIComplete,
			IntervalSeconds:  device.Interval,
			ExpiresInSeconds: device.ExpiresIn,
		})
	}
	interval := device.Interval
	if interval <= 0 {
		interval = kimiDefaultPollSeconds
	}
	expires := device.ExpiresIn
	if expires <= 0 {
		expires = kimiDeviceTimeoutSeconds
	}
	loginSleep := p.sleep
	if loginSleep == nil {
		loginSleep = abortableDeviceSleep
	}
	if err := loginSleep(ctx, time.Duration(interval*float64(time.Second))); err != nil {
		return OAuthCredentials{}, err
	}
	token, err := PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[kimiTokenResponse]{
		IntervalSeconds:  &interval,
		ExpiresInSeconds: &expires,
		Poll: func() (DeviceCodePollResult[kimiTokenResponse], error) {
			return p.pollToken(ctx, device.DeviceCode)
		},
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	return kimiCredentials(token)
}

// RefreshToken refreshes tokens without cancellation. Prefer RefreshTokenContext.
func (p kimiOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return p.RefreshTokenContext(context.Background(), creds)
}

// backoff waits between refresh attempts. A cancelled refresh fails with the cancellation cause, as upstream's sleep(delay, signal) rejects with signal.reason (kimi-coding.ts:218).
func (p kimiOAuthProvider) backoff(ctx context.Context, delay time.Duration) error {
	if p.sleep != nil {
		return p.sleep(ctx, delay)
	}
	return Sleep(ctx, float64(delay)/float64(time.Millisecond))
}

// RefreshTokenContext refreshes tokens and backs off with the owning operation's cancellation (refreshToken, kimi-coding.ts:212-262).
func (p kimiOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	var lastErr error
	for attempt := 0; attempt <= kimiRefreshMaxRetries; attempt++ {
		if attempt > 0 {
			if err := p.backoff(ctx, time.Second*time.Duration(1<<(attempt-1))); err != nil {
				return OAuthCredentials{}, err
			}
		}
		if ctx.Err() != nil {
			return OAuthCredentials{}, errors.New("Kimi Code token refresh aborted")
		}
		raw, status, err := p.tokenRequest(ctx, formFields{
			{"client_id", kimiOAuthClientID},
			{"grant_type", "refresh_token"},
			{"refresh_token", creds.Refresh},
		})
		if err != nil {
			lastErr = err
			continue
		}
		body := readKimiJSON(raw)
		if status >= 200 && status < 300 {
			token, err := parseKimiToken(body, "refresh")
			if err != nil {
				return OAuthCredentials{}, err
			}
			return kimiCredentials(token)
		}
		if failure, _ := body.str("error"); status == http.StatusUnauthorized || status == http.StatusForbidden || failure == "invalid_grant" {
			return OAuthCredentials{}, fmt.Errorf("Kimi Code token refresh unauthorized (status %d)%s", status, body.description())
		}
		if (status == http.StatusTooManyRequests || status >= 500) && attempt < kimiRefreshMaxRetries {
			lastErr = fmt.Errorf("Kimi Code token refresh failed with status %d", status)
			continue
		}
		return OAuthCredentials{}, fmt.Errorf("Kimi Code token refresh failed with status %d: %s", status, body.text)
	}
	if lastErr != nil {
		return OAuthCredentials{}, lastErr
	}
	return OAuthCredentials{}, errors.New("Kimi Code token refresh failed")
}

func (p kimiOAuthProvider) startDeviceAuthorization(ctx context.Context) (kimiDeviceAuthorization, error) {
	if ctx.Err() != nil {
		return kimiDeviceAuthorization{}, ctx.Err()
	}
	requestCtx, cancel := context.WithTimeout(ctx, kimiRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, p.oauthHost+"/api/oauth/device_authorization", strings.NewReader(formFields{{"client_id", kimiOAuthClientID}}.Encode()))
	if err != nil {
		return kimiDeviceAuthorization{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return kimiDeviceAuthorization{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	// A body that fails to read is no body: upstream's response.text() falls back to "" and readJson to null.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		body = nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return kimiDeviceAuthorization{}, fmt.Errorf("Kimi Code device authorization failed with status %d%s", resp.StatusCode, oauthTextSuffix(body))
	}
	payload := readKimiJSON(body)
	deviceCode, okDevice := payload.str("device_code")
	userCode, okUser := payload.str("user_code")
	verificationURI, okURI := payload.str("verification_uri")
	verificationURIComplete, okComplete := payload.str("verification_uri_complete")
	if !okDevice || !okUser || !okURI || !okComplete || !trustedHTTPURL(verificationURIComplete) || !trustedHTTPURL(verificationURI) {
		return kimiDeviceAuthorization{}, fmt.Errorf("Invalid Kimi Code device authorization response: %s", payload.text)
	}
	device := kimiDeviceAuthorization{DeviceCode: deviceCode, UserCode: userCode, VerificationURI: verificationURI, VerificationURIComplete: verificationURIComplete, Interval: kimiDefaultPollSeconds, ExpiresIn: kimiDeviceTimeoutSeconds}
	if interval, ok := payload.num("interval"); ok && !math.IsInf(interval, 0) && interval > 0 {
		device.Interval = interval
	}
	if expiresIn, ok := payload.num("expires_in"); ok && !math.IsInf(expiresIn, 0) && expiresIn > 0 {
		device.ExpiresIn = expiresIn
	}
	return device, nil
}

func (p kimiOAuthProvider) pollToken(ctx context.Context, deviceCode string) (DeviceCodePollResult[kimiTokenResponse], error) {
	raw, status, err := p.tokenRequest(ctx, formFields{
		{"client_id", kimiOAuthClientID},
		{"device_code", deviceCode},
		{"grant_type", "urn:ietf:params:oauth:grant-type:device_code"},
	})
	if err != nil {
		return DeviceCodePollResult[kimiTokenResponse]{}, err
	}
	failed := func(message string) (DeviceCodePollResult[kimiTokenResponse], error) {
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollFailed, Message: message}, nil
	}
	if status >= 500 {
		return failed(fmt.Sprintf("Kimi Code device token request failed with status %d%s", status, oauthTextSuffix(raw)))
	}
	body := readKimiJSON(raw)
	if _, isString := body.str("access_token"); status >= 200 && status < 300 && isString {
		token, err := parseKimiToken(body, "poll")
		if err != nil {
			return failed(err.Error())
		}
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollComplete, Value: token}, nil
	}
	failure, failureIsString := body.str("error")
	switch failure {
	case "authorization_pending":
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollPending}, nil
	case "slow_down":
		result := DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollSlowDown}
		if interval, ok := body.num("interval"); ok && interval > 0 {
			result.IntervalSeconds = &interval
		}
		return result, nil
	case "expired_token":
		return failed("Kimi Code device authorization expired. Please restart login.")
	case "access_denied":
		return failed("Kimi Code login was denied.")
	}
	detail := ""
	if failureIsString {
		detail = ": " + failure + body.description()
	}
	return failed(fmt.Sprintf("Kimi Code device token request failed (status %d)%s", status, detail))
}

// parseKimiToken is parseTokenResponse (kimi-coding.ts:120-137): every field must be present and valid, or the response is reported with its JSON.
func parseKimiToken(body kimiBody, operation string) (kimiTokenResponse, error) {
	access, okAccess := body.str("access_token")
	refresh, okRefresh := body.str("refresh_token")
	expiresIn, okExpires := body.num("expires_in")
	if !okAccess || access == "" || !okRefresh || refresh == "" || !okExpires || math.IsInf(expiresIn, 0) || !(expiresIn > 0) {
		return kimiTokenResponse{}, fmt.Errorf("Kimi Code token %s response missing fields: %s", operation, body.text)
	}
	return kimiTokenResponse{AccessToken: access, RefreshToken: refresh, ExpiresIn: expiresIn}, nil
}

// tokenRequest posts one token request and returns the status and the raw response body.
func (p kimiOAuthProvider) tokenRequest(ctx context.Context, values formFields) ([]byte, int, error) {
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	requestCtx, cancel := context.WithTimeout(ctx, kimiRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, p.oauthHost+"/api/oauth/token", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	// A body that fails to read is no body, as for startDeviceAuthorization: a truncated one is never parsed.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		raw = nil
	}
	return raw, resp.StatusCode, nil
}

func kimiCredentials(token kimiTokenResponse) (OAuthCredentials, error) {
	if token.AccessToken == "" || token.RefreshToken == "" || !(token.ExpiresIn > 0) {
		return OAuthCredentials{}, errors.New("Kimi Code token response missing fields")
	}
	creds := OAuthCredentials{Access: token.AccessToken, Refresh: token.RefreshToken, order: credentialKeyOrder{keys: accessFirstCredentialMembers}}
	creds.SetExpiresMillis(float64(nowMillis()) + float64(token.ExpiresIn*1000))
	return creds, nil
}

// trustedHTTPURL is `trustedHttpUrl(value) !== null` (kimi-coding.ts:59): the verification URI is opened in the user's browser, so only http(s) URLs.
func trustedHTTPURL(value string) bool {
	if value == "" {
		return false
	}
	_, ok := trustedURLHref(value, false)
	return ok
}
