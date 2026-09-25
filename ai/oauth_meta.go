package ai

// Mirrors upstream .upstream/current/packages/ai/src/auth/oauth/meta.ts.
//
// RFC 8628 device authorization grant against https://auth.meta.com (JSON
// responses). Meta splits identity from API access: the identity token is not
// accepted for inference, so it is exchanged for a Model API key via the Muse
// Code key-mint endpoint (minted keys live about a day). The identity token is
// stored as Refresh and the minted key as Access, so the standard OAuth refresh
// re-mints the key when it expires. The identity token itself is not renewable
// (auth.meta.com answers grant_type=refresh_token with 404 and issues no
// refresh_token), so a 401/403 from mint means the session is dead and the
// user must sign in again.

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
	"strings"
	"time"
)

const (
	// Muse Code CLI client id.
	metaClientID               = "1031625952748946"
	metaAuthHost               = "https://auth.meta.com"
	metaDeviceAuthorizationURL = metaAuthHost + "/oidc/device/authorization/"
	metaDeviceTokenURL         = metaAuthHost + "/oidc/device/token/"
	metaAPIKeyMintURL          = "https://api.meta.ai/muse-code/key"
	metaAPIKeyLifetime         = 24 * time.Hour
	metaRequestTimeout         = 30 * time.Second
)

type metaDeviceAuthorization struct {
	deviceCode       string
	userCode         string
	verificationURI  string
	intervalSeconds  *float64
	expiresInSeconds *float64
}

// metaOAuthProvider implements OAuthProviderInterface for Meta's Muse
// subscription. URLs, client, and clock are injectable for tests.
type metaOAuthProvider struct {
	client                 *http.Client
	deviceAuthorizationURL string
	deviceTokenURL         string
	apiKeyMintURL          string
	now                    func() time.Time
}

func newMetaOAuthProvider() metaOAuthProvider {
	return metaOAuthProvider{
		client:                 &http.Client{},
		deviceAuthorizationURL: metaDeviceAuthorizationURL,
		deviceTokenURL:         metaDeviceTokenURL,
		apiKeyMintURL:          metaAPIKeyMintURL,
		now:                    time.Now,
	}
}

func (metaOAuthProvider) ID() string               { return "meta" }
func (metaOAuthProvider) IsSubscription() bool     { return true }
func (metaOAuthProvider) Name() string             { return "Meta (Muse subscription)" }
func (metaOAuthProvider) UsesCallbackServer() bool { return false }

// GetAPIKey returns the minted Model API key, which is the request API key.
func (metaOAuthProvider) GetAPIKey(c OAuthCredentials) string { return c.Access }

// Login runs the flow without cancellation. Prefer LoginContext.
func (p metaOAuthProvider) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.LoginContext(context.Background(), callbacks)
}

// LoginContext runs the device flow and key mint. Cancelling ctx mirrors
// aborting upstream's interaction.signal: any failure after cancellation
// reports "Login cancelled", as loginMeta's catch does.
func (p metaOAuthProvider) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	credentials, err := p.login(ctx, callbacks)
	if err != nil && ctx.Err() != nil {
		return OAuthCredentials{}, errors.New(deviceCodeCancelMessage)
	}
	return credentials, err
}

func (p metaOAuthProvider) login(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	device, err := p.startDeviceAuthorization(ctx)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnDeviceCode != nil {
		info := OAuthDeviceCodeInfo{UserCode: device.userCode, VerificationURI: device.verificationURI}
		if device.intervalSeconds != nil {
			info.IntervalSeconds = *device.intervalSeconds
		}
		if device.expiresInSeconds != nil {
			info.ExpiresInSeconds = *device.expiresInSeconds
		}
		callbacks.OnDeviceCode(info)
	}
	identityToken, err := PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[string]{
		IntervalSeconds:     device.intervalSeconds,
		ExpiresInSeconds:    device.expiresInSeconds,
		WaitBeforeFirstPoll: true,
		Poll:                func() (DeviceCodePollResult[string], error) { return p.pollIdentityToken(ctx, device.deviceCode) },
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Enabling Meta Model API access...")
	}
	return p.mintAPIKey(ctx, identityToken)
}

// RefreshToken re-mints the Model API key without cancellation. Prefer
// RefreshTokenContext.
func (p metaOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	return p.RefreshTokenContext(context.Background(), creds)
}

// RefreshTokenContext re-mints the Model API key from the stored identity
// token with the owning operation's cancellation.
func (p metaOAuthProvider) RefreshTokenContext(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) {
	return p.mintAPIKey(ctx, creds.Refresh)
}

func (p metaOAuthProvider) startDeviceAuthorization(ctx context.Context) (metaDeviceAuthorization, error) {
	status, body, err := p.post(ctx, p.deviceAuthorizationURL, formHeaders(), url.Values{"client_id": {metaClientID}}.Encode())
	if err != nil {
		return metaDeviceAuthorization{}, err
	}
	if !httpStatusOK(status) {
		return metaDeviceAuthorization{}, fmt.Errorf("Meta device authorization failed with status %d%s", status, metaErrorDetail(body))
	}
	deviceCode, _ := body["device_code"].(string)
	userCode, _ := body["user_code"].(string)
	verificationURI := metaTrustedHTTPURL(body["verification_uri_complete"])
	if verificationURI == "" {
		verificationURI = metaTrustedHTTPURL(body["verification_uri"])
	}
	if deviceCode == "" || userCode == "" || verificationURI == "" {
		encoded, _ := json.Marshal(body)
		return metaDeviceAuthorization{}, fmt.Errorf("Invalid Meta device authorization response: %s", encoded)
	}
	return metaDeviceAuthorization{
		deviceCode:       deviceCode,
		userCode:         userCode,
		verificationURI:  verificationURI,
		intervalSeconds:  metaPositiveNumber(body["interval"]),
		expiresInSeconds: metaPositiveNumber(body["expires_in"]),
	}, nil
}

func (p metaOAuthProvider) pollIdentityToken(ctx context.Context, deviceCode string) (DeviceCodePollResult[string], error) {
	status, body, err := p.post(ctx, p.deviceTokenURL, formHeaders(), url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {metaClientID},
	}.Encode())
	if err != nil {
		return DeviceCodePollResult[string]{}, err
	}
	if token, _ := body["access_token"].(string); httpStatusOK(status) && token != "" {
		return DeviceCodePollResult[string]{Status: DevicePollComplete, Value: token}, nil
	}
	errorCode, _ := body["error"].(string)
	switch errorCode {
	case "authorization_pending":
		return DeviceCodePollResult[string]{Status: DevicePollPending}, nil
	case "slow_down":
		return DeviceCodePollResult[string]{Status: DevicePollSlowDown, IntervalSeconds: metaPositiveNumber(body["interval"])}, nil
	case "access_denied":
		return DeviceCodePollResult[string]{Status: DevicePollFailed, Message: "Meta login was denied."}, nil
	case "expired_token":
		return DeviceCodePollResult[string]{Status: DevicePollFailed, Message: "Meta device authorization expired. Please restart login."}, nil
	default:
		return DeviceCodePollResult[string]{
			Status:  DevicePollFailed,
			Message: fmt.Sprintf("Meta device token request failed with status %d%s", status, metaErrorDetail(body)),
		}, nil
	}
}

// mintAPIKey exchanges an identity token for a Model API key.
func (p metaOAuthProvider) mintAPIKey(ctx context.Context, identityToken string) (OAuthCredentials, error) {
	headers := map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + identityToken,
		"Content-Type":  "application/json",
		"x-api-version": "1.0.0",
	}
	status, body, err := p.post(ctx, p.apiKeyMintURL, headers, "{}")
	if err != nil {
		return OAuthCredentials{}, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		// The identity token is not renewable; only a fresh device flow helps.
		return OAuthCredentials{}, fmt.Errorf("Meta session expired (status %d). Run `/login meta` to sign in again.%s", status, metaErrorDetail(body))
	}
	if !httpStatusOK(status) {
		return OAuthCredentials{}, fmt.Errorf("Meta API key mint failed with status %d%s", status, metaErrorDetail(body))
	}
	apiKey, _ := body["api_key"].(string)
	if apiKey == "" {
		message := "Meta did not issue an API key."
		if actionURL := metaTrustedHTTPURL(body["action_url"]); actionURL != "" {
			message += " Complete setup at " + actionURL
		}
		return OAuthCredentials{}, errors.New(message)
	}
	return OAuthCredentials{
		Refresh: identityToken,
		Access:  apiKey,
		Expires: p.now().Add(metaAPIKeyLifetime).UnixMilli(),
	}, nil
}

// post sends one request and decodes a JSON object body; a body that is not
// a JSON object decodes as nil, as upstream readJson does. A cancelled ctx
// sends nothing, as fetch rejects an already-aborted signal.
func (p metaOAuthProvider) post(ctx context.Context, endpoint string, headers map[string]string, body string) (int, map[string]any, error) {
	if ctx.Err() != nil {
		return 0, nil, errors.New(deviceCodeCancelMessage)
	}
	requestCtx, cancel := context.WithTimeout(ctx, metaRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader([]byte(body)))
	if err != nil {
		return 0, nil, err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := p.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, errors.New(deviceCodeCancelMessage)
		}
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(response.Body)
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		decoded = nil
	}
	return response.StatusCode, decoded, nil
}

func formHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"}
}

func httpStatusOK(status int) bool { return status >= 200 && status < 300 }

func metaErrorDetail(body map[string]any) string {
	for _, key := range []string{"error_description", "detail", "message", "error"} {
		if value, ok := body[key].(string); ok && strings.TrimSpace(value) != "" {
			return ": " + strings.TrimSpace(value)
		}
	}
	return ""
}

// metaTrustedHTTPURL accepts only http(s) URLs: the verification URI is
// opened in the user's browser.
func metaTrustedHTTPURL(value any) string {
	raw, _ := value.(string)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func metaPositiveNumber(value any) *float64 {
	number, ok := value.(float64)
	if !ok || math.IsInf(number, 0) || math.IsNaN(number) || number <= 0 {
		return nil
	}
	return &number
}
