package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
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

type kimiDeviceAuthorization struct {
	DeviceCode              string  `json:"device_code"`
	UserCode                string  `json:"user_code"`
	VerificationURI         string  `json:"verification_uri"`
	VerificationURIComplete string  `json:"verification_uri_complete"`
	Interval                float64 `json:"interval"`
	ExpiresIn               float64 `json:"expires_in"`
}

type kimiTokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    float64 `json:"expires_in"`
	Error        string  `json:"error"`
	Description  string  `json:"error_description"`
	Interval     float64 `json:"interval"`
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
		sleep:     abortableDeviceSleep,
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
	if err := p.sleep(ctx, time.Duration(interval*float64(time.Second))); err != nil {
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

// RefreshTokenContext refreshes tokens and backs off with the owning operation's cancellation.
func (p kimiOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	if creds.Refresh == "" {
		return OAuthCredentials{}, errors.New("Kimi Code token refresh requires a refresh token")
	}
	var lastErr error
	for attempt := 0; attempt <= kimiRefreshMaxRetries; attempt++ {
		if ctx.Err() != nil {
			return OAuthCredentials{}, errors.New("Kimi Code token refresh aborted")
		}
		if attempt > 0 {
			if err := p.sleep(ctx, time.Second*time.Duration(1<<(attempt-1))); err != nil {
				return OAuthCredentials{}, errors.New("Kimi Code token refresh aborted")
			}
		}
		response, status, err := p.tokenRequest(ctx, url.Values{
			"client_id":     {kimiOAuthClientID},
			"grant_type":    {"refresh_token"},
			"refresh_token": {creds.Refresh},
		})
		if err != nil {
			lastErr = err
			continue
		}
		if status >= 200 && status < 300 {
			return kimiCredentials(response)
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden || response.Error == "invalid_grant" {
			return OAuthCredentials{}, fmt.Errorf("Kimi Code token refresh unauthorized (status %d)%s", status, kimiDescription(response.Description))
		}
		if (status == http.StatusTooManyRequests || status >= 500) && attempt < kimiRefreshMaxRetries {
			lastErr = fmt.Errorf("Kimi Code token refresh failed with status %d", status)
			continue
		}
		return OAuthCredentials{}, fmt.Errorf("Kimi Code token refresh failed with status %d%s", status, kimiDescription(response.Description))
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
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, p.oauthHost+"/api/oauth/device_authorization", strings.NewReader(url.Values{"client_id": {kimiOAuthClientID}}.Encode()))
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return kimiDeviceAuthorization{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return kimiDeviceAuthorization{}, fmt.Errorf("Kimi Code device authorization failed with status %d%s", resp.StatusCode, kimiBodySuffix(body))
	}
	var device kimiDeviceAuthorization
	if err := json.Unmarshal(body, &device); err != nil {
		return kimiDeviceAuthorization{}, fmt.Errorf("invalid Kimi Code device authorization response: %w", err)
	}
	if device.DeviceCode == "" || device.UserCode == "" || !trustedHTTPURL(device.VerificationURI) || !trustedHTTPURL(device.VerificationURIComplete) {
		return kimiDeviceAuthorization{}, fmt.Errorf("invalid Kimi Code device authorization response: %s", body)
	}
	if device.Interval <= 0 {
		device.Interval = kimiDefaultPollSeconds
	}
	if device.ExpiresIn <= 0 {
		device.ExpiresIn = kimiDeviceTimeoutSeconds
	}
	return device, nil
}

func (p kimiOAuthProvider) pollToken(ctx context.Context, deviceCode string) (DeviceCodePollResult[kimiTokenResponse], error) {
	response, status, err := p.tokenRequest(ctx, url.Values{
		"client_id":   {kimiOAuthClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	})
	if err != nil {
		return DeviceCodePollResult[kimiTokenResponse]{}, err
	}
	if status >= 500 {
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollFailed, Message: fmt.Sprintf("Kimi Code device token request failed with status %d", status)}, nil
	}
	if status >= 200 && status < 300 && response.AccessToken != "" {
		if _, err := kimiCredentials(response); err != nil {
			return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollFailed, Message: err.Error()}, nil
		}
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollComplete, Value: response}, nil
	}
	switch response.Error {
	case "authorization_pending":
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollPending}, nil
	case "slow_down":
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollSlowDown}, nil
	case "expired_token":
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollFailed, Message: "Kimi Code device authorization expired. Please restart login."}, nil
	case "access_denied":
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollFailed, Message: "Kimi Code login was denied."}, nil
	default:
		return DeviceCodePollResult[kimiTokenResponse]{Status: DevicePollFailed, Message: fmt.Sprintf("Kimi Code device token request failed (status %d): %s%s", status, response.Error, kimiDescription(response.Description))}, nil
	}
}

func (p kimiOAuthProvider) tokenRequest(ctx context.Context, values url.Values) (kimiTokenResponse, int, error) {
	if ctx.Err() != nil {
		return kimiTokenResponse{}, 0, ctx.Err()
	}
	requestCtx, cancel := context.WithTimeout(ctx, kimiRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, p.oauthHost+"/api/oauth/token", strings.NewReader(values.Encode()))
	if err != nil {
		return kimiTokenResponse{}, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return kimiTokenResponse{}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var token kimiTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return kimiTokenResponse{}, resp.StatusCode, err
	}
	return token, resp.StatusCode, nil
}

func kimiCredentials(token kimiTokenResponse) (OAuthCredentials, error) {
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 {
		return OAuthCredentials{}, errors.New("Kimi Code token response missing fields")
	}
	return OAuthCredentials{
		Access:  token.AccessToken,
		Refresh: token.RefreshToken,
		Expires: time.Now().Add(time.Duration(token.ExpiresIn * float64(time.Second))).UnixMilli(),
	}, nil
}

func trustedHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http")
}

func kimiDescription(value string) string {
	if value == "" {
		return ""
	}
	return ": " + value
}

func kimiBodySuffix(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return ": " + string(body)
}
