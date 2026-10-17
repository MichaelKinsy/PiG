package ai

// Mirrors upstream .upstream/current/packages/ai/src/auth/oauth/xai.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

const (
	xaiClientID                   = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiScope                      = "openid profile email offline_access grok-cli:access api:access"
	xaiDeviceCodeURL              = "https://auth.x.ai/oauth2/device/code"
	xaiTokenURL                   = "https://auth.x.ai/oauth2/token"
	xaiRequestTimeout             = 30 * time.Second
	xaiRefreshSkewMs        int64 = 5 * 60 * 1000
	xaiDefaultTokenLifetime int64 = 3600
	xaiDeviceGrantType            = "urn:ietf:params:oauth:grant-type:device_code"
)

// xaiBody is the union of the device-authorization and token response fields.
// Pointers mark fields whose absence is meaningful (token expires_in defaults;
// a rotated refresh token may be omitted).
type xaiBody struct {
	DeviceCode              string   `json:"device_code"`
	UserCode                string   `json:"user_code"`
	VerificationURI         string   `json:"verification_uri"`
	VerificationURIComplete string   `json:"verification_uri_complete"`
	Interval                *float64 `json:"interval"`
	ExpiresIn               *float64 `json:"expires_in"`
	AccessToken             string   `json:"access_token"`
	RefreshToken            *string  `json:"refresh_token"`
	Error                   string   `json:"error"`
	ErrorDescription        string   `json:"error_description"`
}

type xaiDeviceCode struct {
	deviceCode              string
	userCode                string
	verificationURI         string
	verificationURIComplete string
	intervalSeconds         *float64
	expiresInSeconds        float64
}

// xaiOAuthProvider implements OAuthProviderInterface for xAI's device-code
// subscription login. URLs, client, and clock are injectable for tests.
type xaiOAuthProvider struct {
	client    *http.Client
	deviceURL string
	tokenURL  string
	now       func() time.Time
}

func newXaiOAuthProvider() xaiOAuthProvider {
	return xaiOAuthProvider{
		client:    &http.Client{},
		deviceURL: xaiDeviceCodeURL,
		tokenURL:  xaiTokenURL,
		now:       time.Now,
	}
}

func (xaiOAuthProvider) ID() string                          { return "xai" }
func (xaiOAuthProvider) IsSubscription() bool                { return true }
func (xaiOAuthProvider) Name() string                        { return "xAI" }
func (xaiOAuthProvider) UsesCallbackServer() bool            { return false }
func (xaiOAuthProvider) GetAPIKey(c OAuthCredentials) string { return c.Access }

// Login runs the flow without cancellation. Prefer LoginContext.
func (p xaiOAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.LoginContext(context.Background(), callbacks)
}

// LoginContext requests a device code and polls for tokens with the owning operation's cancellation.
func (p xaiOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	status, body, err := p.postForm(ctx, p.deviceURL, formFields{
		{"client_id", xaiClientID},
		{"scope", xaiScope},
		// pig divergence (D26): PiG names itself as the device-code referrer.
		{"referrer", pigidentity.XAIReferrer},
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	if status < 200 || status >= 300 {
		return OAuthCredentials{}, xaiRequestFailure("device authorization", status, body)
	}
	device, err := parseXaiDeviceCode(body)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnDeviceCode != nil {
		verURI := device.verificationURI
		if device.verificationURIComplete != "" {
			verURI = device.verificationURIComplete
		}
		var interval float64
		if device.intervalSeconds != nil {
			interval = *device.intervalSeconds
		}
		callbacks.OnDeviceCode(OAuthDeviceCodeInfo{
			UserCode:         device.userCode,
			VerificationURI:  verURI,
			IntervalSeconds:  interval,
			ExpiresInSeconds: device.expiresInSeconds,
		})
	}

	expires := device.expiresInSeconds
	return PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[OAuthCredentials]{
		IntervalSeconds:     device.intervalSeconds,
		ExpiresInSeconds:    &expires,
		WaitBeforeFirstPoll: true,
		Poll: func() (DeviceCodePollResult[OAuthCredentials], error) {
			return p.pollToken(ctx, device.deviceCode)
		},
	})
}

// RefreshToken refreshes tokens without cancellation. Prefer RefreshTokenContext.
func (p xaiOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return p.RefreshTokenContext(context.Background(), creds)
}

// RefreshTokenContext refreshes tokens with the owning operation's cancellation.
func (p xaiOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	status, body, err := p.postForm(ctx, p.tokenURL, formFields{
		{"grant_type", "refresh_token"},
		{"client_id", xaiClientID},
		{"refresh_token", creds.Refresh},
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	if status < 200 || status >= 300 {
		return OAuthCredentials{}, xaiRequestFailure("token refresh", status, body)
	}
	return p.credentialsFromToken(body, creds.Refresh)
}

func (p xaiOAuthProvider) pollToken(ctx context.Context, deviceCode string) (DeviceCodePollResult[OAuthCredentials], error) {
	status, body, err := p.postForm(ctx, p.tokenURL, formFields{
		{"grant_type", xaiDeviceGrantType},
		{"client_id", xaiClientID},
		{"device_code", deviceCode},
	})
	if err != nil {
		return DeviceCodePollResult[OAuthCredentials]{}, err
	}
	if status >= 200 && status < 300 {
		// A malformed success response is a hard error, mirroring upstream where
		// credentialsFromTokenResponse throws out of the poll rather than pending.
		creds, cerr := p.credentialsFromToken(body, "")
		if cerr != nil {
			return DeviceCodePollResult[OAuthCredentials]{}, cerr
		}
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollComplete, Value: creds}, nil
	}
	switch body.Error {
	case "authorization_pending":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollPending}, nil
	case "slow_down":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollSlowDown, IntervalSeconds: body.Interval}, nil
	case "access_denied", "authorization_denied":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollFailed, Message: "xAI device authorization was denied"}, nil
	case "expired_token":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollFailed, Message: "xAI device code expired"}, nil
	default:
		return DeviceCodePollResult[OAuthCredentials]{
			Status:  DevicePollFailed,
			Message: xaiRequestFailure("device token polling", status, body).Error(),
		}, nil
	}
}

func (p xaiOAuthProvider) postForm(ctx context.Context, endpoint string, fields formFields) (int, xaiBody, error) {
	if ctx.Err() != nil {
		return 0, xaiBody{}, errors.New(deviceCodeCancelMessage)
	}
	reqCtx, cancel := context.WithTimeout(ctx, xaiRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(fields.Encode()))
	if err != nil {
		return 0, xaiBody{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, xaiBody{}, errors.New("Login cancelled")
		}
		return 0, xaiBody{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	// response.json() rejects when the body fails to read (a truncated body included), as when it is not JSON.
	raw, readErr := io.ReadAll(resp.Body)
	body, ok := decodeXaiBody(raw)
	if readErr != nil || !ok {
		if ctx.Err() != nil {
			return resp.StatusCode, xaiBody{}, errors.New("Login cancelled")
		}
		return resp.StatusCode, xaiBody{}, fmt.Errorf("xAI OAuth returned invalid JSON (HTTP %d)", resp.StatusCode)
	}
	return resp.StatusCode, body, nil
}

// decodeXaiBody reads a response body as postForm does upstream (xai.ts:61-95): Response.json() (fetchBodyText: a leading byte order mark is dropped and ill-formed UTF-8 replaced as the fetch decoder does), a body that is not a JSON object counts as {}, and each field is typed by what its value is, not by the field's declared type. A wrong-typed string field reads as empty, a wrong-typed number as absent (interval) or invalid (expires_in), and a present refresh_token that is not a string as an empty one: the response validators then report those the way requiredString and positiveNumber do.
func decodeXaiBody(raw []byte) (xaiBody, bool) {
	raw = []byte(fetchBodyText(raw))
	if !json.Valid(raw) {
		return xaiBody{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return xaiBody{}, false
	}
	object, _ := parsed.(map[string]any)
	text := func(key string) string {
		value, _ := object[key].(string)
		return value
	}
	// number is a JSON number's JavaScript value: past the double range it is infinite, as JSON.parse reads it.
	number := func(key string) (float64, bool) {
		value, isNumber := object[key].(json.Number)
		if !isNumber {
			return 0, false
		}
		parsedNumber, _ := strconv.ParseFloat(string(value), 64)
		return parsedNumber, true
	}
	body := xaiBody{
		DeviceCode: text("device_code"), UserCode: text("user_code"), VerificationURI: text("verification_uri"), VerificationURIComplete: text("verification_uri_complete"),
		AccessToken: text("access_token"), Error: text("error"), ErrorDescription: text("error_description"),
	}
	if interval, isNumber := number("interval"); isNumber {
		body.Interval = &interval
	}
	if _, present := object["expires_in"]; present {
		expires, isNumber := number("expires_in")
		if !isNumber {
			expires = 0
		}
		body.ExpiresIn = &expires
	}
	if value, present := object["refresh_token"]; present {
		refresh, _ := value.(string)
		body.RefreshToken = &refresh
	}
	return body, true
}

func (p xaiOAuthProvider) credentialsFromToken(body xaiBody, previousRefresh string) (OAuthCredentials, error) {
	if body.AccessToken == "" {
		return OAuthCredentials{}, errors.New("Invalid xAI OAuth response field: access_token")
	}
	refresh := previousRefresh
	if body.RefreshToken != nil {
		if *body.RefreshToken == "" {
			return OAuthCredentials{}, errors.New("Invalid xAI OAuth response field: refresh_token")
		}
		refresh = *body.RefreshToken
	} else if previousRefresh == "" {
		// xAI omits refresh_token only on a non-rotating refresh; a fresh login
		// must carry one.
		return OAuthCredentials{}, errors.New("Invalid xAI OAuth response field: refresh_token")
	}
	expiresIn := float64(xaiDefaultTokenLifetime)
	if body.ExpiresIn != nil {
		if !(*body.ExpiresIn > 0) || math.IsInf(*body.ExpiresIn, 0) {
			return OAuthCredentials{}, errors.New("Invalid xAI OAuth response field: expires_in")
		}
		expiresIn = *body.ExpiresIn
	}
	creds := OAuthCredentials{Access: body.AccessToken, Refresh: refresh, order: credentialKeyOrder{keys: accessFirstCredentialMembers}}
	creds.SetExpiresMillis(float64(p.now().UnixMilli()) + float64(expiresIn*1000) - float64(xaiRefreshSkewMs))
	return creds, nil
}

func parseXaiDeviceCode(body xaiBody) (xaiDeviceCode, error) {
	// Upstream builds the result object in member order (xai.ts:108-126): verification_uri_complete is checked before the object literal runs, then device_code, user_code, verification_uri and expires_in, and the first failure is the one reported.
	var verComplete string
	if body.VerificationURIComplete != "" {
		var err error
		if verComplete, err = xaiValidateHTTPSURL(body.VerificationURIComplete); err != nil {
			return xaiDeviceCode{}, err
		}
	}
	if body.DeviceCode == "" {
		return xaiDeviceCode{}, errors.New("Invalid xAI OAuth response field: device_code")
	}
	if body.UserCode == "" {
		return xaiDeviceCode{}, errors.New("Invalid xAI OAuth response field: user_code")
	}
	if body.VerificationURI == "" {
		return xaiDeviceCode{}, errors.New("Invalid xAI OAuth response field: verification_uri")
	}
	verURI, err := xaiValidateHTTPSURL(body.VerificationURI)
	if err != nil {
		return xaiDeviceCode{}, err
	}
	if body.ExpiresIn == nil || !(*body.ExpiresIn > 0) || math.IsInf(*body.ExpiresIn, 0) {
		return xaiDeviceCode{}, errors.New("Invalid xAI OAuth response field: expires_in")
	}
	var interval *float64
	if body.Interval != nil && *body.Interval > 0 && !math.IsInf(*body.Interval, 0) {
		interval = body.Interval
	}
	return xaiDeviceCode{
		deviceCode:              body.DeviceCode,
		userCode:                body.UserCode,
		verificationURI:         verURI,
		verificationURIComplete: verComplete,
		intervalSeconds:         interval,
		expiresInSeconds:        *body.ExpiresIn,
	}, nil
}

// xaiValidateHTTPSURL forces the browser verification URI to be https so a
// malicious response cannot make the launcher open an untrusted scheme.
func xaiValidateHTTPSURL(raw string) (string, error) {
	href, ok := trustedURLHref(raw, true)
	if !ok {
		return "", errors.New("Untrusted verification URI in xAI OAuth response")
	}
	return href, nil
}

func xaiRequestFailure(action string, status int, body xaiBody) error {
	detail := body.Error
	if body.ErrorDescription != "" {
		if detail != "" {
			detail += ": " + body.ErrorDescription
		} else {
			detail = body.ErrorDescription
		}
	}
	if detail != "" {
		return fmt.Errorf("xAI OAuth %s failed (HTTP %d): %s", action, status, detail)
	}
	return fmt.Errorf("xAI OAuth %s failed (HTTP %d)", action, status)
}
