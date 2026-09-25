package ai

// Mirrors upstream .upstream/current/packages/ai/src/auth/oauth/radius.ts.
//
// Radius is a pi-messages gateway. OAuth client APIs live on the configured
// gateway; only the interactive browser authorization endpoint is discovered.
// Model catalog loading is owned by the Radius provider (radius.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	radiusCallbackHost          = "127.0.0.1"
	radiusCallbackPort          = 1456
	radiusCallbackPath          = "/oauth/callback"
	radiusTokenExpirySkew       = 60 * time.Second
	RadiusLoginMethodBrowser    = "browser"
	RadiusLoginMethodDeviceCode = "device-code"
	radiusOAuthClientID         = "pi-gateway"
	radiusOAuthScope            = "gateway offline_access"
	radiusDeviceCodeGrantType   = "urn:ietf:params:oauth:grant-type:device_code"
	radiusLoginCancelled        = "Login cancelled"
)

// RadiusOAuthOptions configures CreateRadiusOAuth. ID is the provider ID the
// flow is registered under; Pi derives it from the owning provider.
type RadiusOAuthOptions struct {
	ID      string
	Name    string
	Gateway string
}

// RadiusOAuth is the Radius gateway OAuth flow (browser PKCE or device code).
type RadiusOAuth struct {
	id      string
	name    string
	gateway string
	// callbackAddress is the loopback listen address; the redirect URI names it.
	callbackAddress string
	now             func() time.Time
}

// CreateRadiusOAuth mirrors upstream createRadiusOAuth.
func CreateRadiusOAuth(options RadiusOAuthOptions) *RadiusOAuth {
	id := options.ID
	if id == "" {
		id = "radius"
	}
	return &RadiusOAuth{
		id:              id,
		name:            options.Name,
		gateway:         NormalizeRadiusGatewayURL(options.Gateway),
		callbackAddress: fmt.Sprintf("%s:%d", radiusCallbackHost, radiusCallbackPort),
		now:             time.Now,
	}
}

func (o *RadiusOAuth) ID() string                                    { return o.id }
func (o *RadiusOAuth) Name() string                                  { return o.name }
func (o *RadiusOAuth) UsesCallbackServer() bool                      { return true }
func (o *RadiusOAuth) GetAPIKey(credentials OAuthCredentials) string { return credentials.Access }

// Gateway returns the normalized gateway origin used for every OAuth request.
func (o *RadiusOAuth) Gateway() string { return o.gateway }

// LoginMethodPrompt is the sign-in method selection Pi presents before login.
func (o *RadiusOAuth) LoginMethodPrompt() OAuthSelectPrompt {
	return OAuthSelectPrompt{
		Message: fmt.Sprintf("Sign in to %s:", o.name),
		Options: []OAuthSelectOption{
			{ID: RadiusLoginMethodBrowser, Label: "Sign in with browser (recommended)"},
			{ID: RadiusLoginMethodDeviceCode, Label: "Sign in with device code (when signing in from another device)"},
		},
	}
}

// Login runs the flow without cancellation. Prefer LoginContext.
func (o *RadiusOAuth) Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return o.LoginContext(context.Background(), callbacks)
}

// LoginContext asks for a sign-in method, then runs the browser or device flow.
// Cancelling ctx mirrors aborting upstream's interaction.signal.
func (o *RadiusOAuth) LoginContext(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	method := ""
	if callbacks.OnSelect != nil {
		selected, err := callbacks.OnSelect(o.LoginMethodPrompt())
		if err != nil {
			return OAuthCredentials{}, err
		}
		method = selected
	}
	switch method {
	case RadiusLoginMethodDeviceCode:
		return o.loginWithDeviceCode(ctx, callbacks)
	case RadiusLoginMethodBrowser:
		discovery, err := o.loadOAuthDiscovery(ctx)
		if err != nil {
			return OAuthCredentials{}, err
		}
		return o.loginWithBrowser(ctx, discovery, callbacks)
	}
	return OAuthCredentials{}, fmt.Errorf("Unknown %s sign-in method: %s", o.name, method)
}

// RefreshToken exchanges the refresh token directly at the gateway.
func (o *RadiusOAuth) RefreshToken(credentials OAuthCredentials) (OAuthCredentials, error) {
	return o.RefreshTokenContext(context.Background(), credentials)
}

// RefreshTokenContext exchanges the token with the owning operation's cancellation.
func (o *RadiusOAuth) RefreshTokenContext(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
	return o.requestOAuthToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {radiusOAuthClientID},
		"refresh_token": {credentials.Refresh},
	})
}

func (o *RadiusOAuth) redirectURI() string {
	return "http://" + o.callbackAddress + radiusCallbackPath
}

func (o *RadiusOAuth) loadOAuthDiscovery(ctx context.Context) (string, error) {
	status, body, err := o.send(ctx, http.MethodGet, "/v1/oauth", nil)
	if err != nil {
		return "", err
	}
	if status < 200 || status > 299 {
		return "", fmt.Errorf("Could not load Radius OAuth config from %s: %d %s", o.gateway, status, body)
	}
	var discovery map[string]json.RawMessage
	var endpoint string
	if json.Unmarshal(body, &discovery) != nil || json.Unmarshal(discovery["authorizationEndpoint"], &endpoint) != nil {
		return "", fmt.Errorf("Invalid Radius OAuth config from %s", o.gateway)
	}
	return endpoint, nil
}

// send issues one gateway request and reads the whole body.
func (o *RadiusOAuth) send(ctx context.Context, method, path string, form url.Values) (int, []byte, error) {
	endpoint, err := resolveGatewayPath(o.gateway, path)
	if err != nil {
		return 0, nil, err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("accept", "application/json")
	if form != nil {
		request.Header.Set("content-type", "application/x-www-form-urlencoded")
	}
	response, err := radiusHTTPClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

// sendForm posts form data, mapping a cancelled request to "Login cancelled".
func (o *RadiusOAuth) sendForm(ctx context.Context, path string, form url.Values) (int, []byte, error) {
	status, body, err := o.send(ctx, http.MethodPost, path, form)
	if err != nil && ctx.Err() != nil {
		return 0, nil, errors.New(radiusLoginCancelled)
	}
	return status, body, err
}

// RadiusOAuthResponseError is a non-2xx OAuth response. Mirrors upstream
// OAuthResponseError.
type RadiusOAuthResponseError struct {
	Status     int
	OAuthError string
	message    string
}

func (e *RadiusOAuthResponseError) Error() string { return e.message }

func readRadiusOAuthResponseError(status int, body []byte, message string) *RadiusOAuthResponseError {
	var oauthError, description string
	if text := string(body); text != "" {
		var data map[string]any
		if json.Unmarshal(body, &data) == nil && data != nil {
			oauthError, _ = data["error"].(string)
			description, _ = data["error_description"].(string)
		} else {
			description = text
		}
	}
	detail := description
	switch {
	case oauthError != "" && description != "":
		detail = oauthError + ": " + description
	case oauthError != "":
		detail = oauthError
	case description == "":
		detail = fmt.Sprint(status)
	}
	return &RadiusOAuthResponseError{Status: status, OAuthError: oauthError, message: message + ": " + detail}
}

func (o *RadiusOAuth) requestOAuthToken(ctx context.Context, form url.Values) (OAuthCredentials, error) {
	status, body, err := o.sendForm(ctx, "/v1/oauth/token", form)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if status < 200 || status > 299 {
		return OAuthCredentials{}, readRadiusOAuthResponseError(status, body, "Radius OAuth token request failed")
	}
	var data struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
		Scope        string  `json:"scope"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return OAuthCredentials{}, err
	}
	expires := o.now().UnixMilli() + int64(data.ExpiresIn*1000) - radiusTokenExpirySkew.Milliseconds()
	return OAuthCredentials{Access: data.AccessToken, Refresh: data.RefreshToken, Expires: expires, Scope: data.Scope}, nil
}

func (o *RadiusOAuth) loginWithBrowser(ctx context.Context, authorizationEndpoint string, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, err
	}
	state := uuid.NewString()
	authorizeURL, err := url.Parse(authorizationEndpoint)
	if err != nil {
		return OAuthCredentials{}, err
	}
	authorizeURL.RawQuery = orderedQuery(
		"response_type", "code", "client_id", radiusOAuthClientID, "redirect_uri", o.redirectURI(),
		"scope", radiusOAuthScope, "code_challenge", pkce.Challenge, "code_challenge_method", "S256",
		"handoff", "url", "state", state,
	)
	server := o.startCallbackServer(ctx, state)
	defer server.close()
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Listening for OAuth callback on " + o.redirectURI())
	}
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{URL: authorizeURL.String(), Instructions: "Continue in your browser."})
	}
	code := server.waitForCode()
	if code == "" {
		if ctx.Err() != nil {
			return OAuthCredentials{}, errors.New(radiusLoginCancelled)
		}
		return OAuthCredentials{}, errors.New("OAuth callback did not complete.")
	}
	return o.requestOAuthToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {radiusOAuthClientID},
		"redirect_uri":  {o.redirectURI()},
		"code":          {code},
		"code_verifier": {pkce.Verifier},
	})
}

// orderedQuery encodes key/value pairs in the given order, like
// URLSearchParams.toString (url.Values.Encode sorts keys).
func orderedQuery(pairs ...string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, url.QueryEscape(pairs[i])+"="+url.QueryEscape(pairs[i+1]))
	}
	return strings.Join(parts, "&")
}

// radiusCallbackServer is the loopback OAuth callback listener. When the
// port cannot be bound, waitForCode reports no code, as upstream does.
type radiusCallbackServer struct {
	once   sync.Once
	result chan string
	server *http.Server
	stop   context.CancelFunc
}

func (s *radiusCallbackServer) finish(code string) {
	s.once.Do(func() { s.result <- code })
}

func (s *radiusCallbackServer) waitForCode() string { return <-s.result }

func (s *radiusCallbackServer) close() {
	s.finish("")
	s.stop()
	if s.server != nil {
		_ = s.server.Close()
	}
}

func (o *RadiusOAuth) startCallbackServer(ctx context.Context, expectedState string) *radiusCallbackServer {
	watchCtx, stop := context.WithCancel(ctx)
	callback := &radiusCallbackServer{result: make(chan string, 1), stop: stop}
	go func() {
		<-watchCtx.Done()
		callback.finish("")
	}()
	listener, err := net.Listen("tcp", o.callbackAddress)
	if err != nil {
		callback.finish("")
		return callback
	}
	callback.server = &http.Server{Handler: radiusCallbackHandler(expectedState, callback.finish), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = callback.server.Serve(listener) }()
	return callback
}

func radiusCallbackHandler(expectedState string, finish func(string)) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		switch {
		case request.URL.Path != radiusCallbackPath:
			sendRadiusCallbackPage(writer, http.StatusNotFound, OAuthErrorHTML("Callback route not found.", ""))
		case query.Get("state") != expectedState:
			sendRadiusCallbackPage(writer, http.StatusBadRequest, OAuthErrorHTML("OAuth state mismatch.", ""))
		case query.Get("error") != "":
			message := query.Get("error")
			if query.Has("error_description") {
				message = query.Get("error_description")
			}
			sendRadiusCallbackPage(writer, http.StatusBadRequest, OAuthErrorHTML(message, ""))
			finish("")
		case query.Get("code") == "":
			sendRadiusCallbackPage(writer, http.StatusBadRequest, OAuthErrorHTML("Missing authorization code.", ""))
		default:
			sendRadiusCallbackPage(writer, http.StatusOK, OAuthSuccessHTML("Signed in to Radius. You may now close this page."))
			finish(query.Get("code"))
		}
	})
}

func sendRadiusCallbackPage(writer http.ResponseWriter, status int, html string) {
	writer.Header().Set("content-type", "text/html; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, html)
}

func (o *RadiusOAuth) requestDeviceAuthorization(ctx context.Context) (radiusDeviceAuthorization, error) {
	status, body, err := o.sendForm(ctx, "/v1/oauth/device", url.Values{"client_id": {radiusOAuthClientID}, "scope": {radiusOAuthScope}})
	if err != nil {
		return radiusDeviceAuthorization{}, err
	}
	if status < 200 || status > 299 {
		return radiusDeviceAuthorization{}, readRadiusOAuthResponseError(status, body, "Radius OAuth device authorization failed")
	}
	var device radiusDeviceAuthorization
	if err := json.Unmarshal(body, &device); err != nil {
		return radiusDeviceAuthorization{}, err
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.VerificationURI == "" || device.ExpiresIn == 0 {
		return radiusDeviceAuthorization{}, errors.New("Radius OAuth device authorization response is missing required fields")
	}
	return device, nil
}

type radiusDeviceAuthorization struct {
	DeviceCode      string   `json:"device_code"`
	UserCode        string   `json:"user_code"`
	VerificationURI string   `json:"verification_uri"`
	ExpiresIn       float64  `json:"expires_in"`
	Interval        *float64 `json:"interval"`
}

func (o *RadiusOAuth) loginWithDeviceCode(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	device, err := o.requestDeviceAuthorization(ctx)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if callbacks.OnDeviceCode != nil {
		info := OAuthDeviceCodeInfo{UserCode: device.UserCode, VerificationURI: device.VerificationURI, ExpiresInSeconds: device.ExpiresIn}
		if device.Interval != nil {
			info.IntervalSeconds = *device.Interval
		}
		callbacks.OnDeviceCode(info)
	}
	form := url.Values{"grant_type": {radiusDeviceCodeGrantType}, "client_id": {radiusOAuthClientID}, "device_code": {device.DeviceCode}}
	return PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[OAuthCredentials]{
		IntervalSeconds:  device.Interval,
		ExpiresInSeconds: &device.ExpiresIn,
		Poll: func() (DeviceCodePollResult[OAuthCredentials], error) {
			credentials, err := o.requestOAuthToken(ctx, form)
			if err == nil {
				return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollComplete, Value: credentials}, nil
			}
			return radiusDevicePollResult(err)
		},
	})
}

func radiusDevicePollResult(err error) (DeviceCodePollResult[OAuthCredentials], error) {
	var responseError *RadiusOAuthResponseError
	if !errors.As(err, &responseError) {
		return DeviceCodePollResult[OAuthCredentials]{}, err
	}
	switch responseError.OAuthError {
	case "authorization_pending":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollPending}, nil
	case "slow_down":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollSlowDown}, nil
	case "expired_token":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollFailed, Message: "Device authorization expired."}, nil
	case "access_denied":
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollFailed, Message: "Device authorization was denied."}, nil
	}
	return DeviceCodePollResult[OAuthCredentials]{}, err
}
