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
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
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
	return o.requestOAuthToken(ctx, formFields{
		{"grant_type", "refresh_token"},
		{"client_id", radiusOAuthClientID},
		{"refresh_token", credentials.Refresh},
	})
}

func (o *RadiusOAuth) redirectURI() string {
	return "http://" + o.callbackAddress + radiusCallbackPath
}

// radiusReply is one gateway response. readErr is the failure to read its body: Node's fetch rejects text() and json() then.
type radiusReply struct {
	status  int
	body    []byte
	readErr error
}

func (r radiusReply) ok() bool { return r.status >= 200 && r.status <= 299 }

// json is `await response.json()` of a successful response, which radius.ts does not catch: a body that failed to read rejects with fetch's error and one that is not JSON with V8's SyntaxError. A null body has no members, so reading one throws a TypeError (member names the property the caller reads).
func (r radiusReply) json(ctx context.Context, member string) (map[string]any, error) {
	if r.readErr != nil {
		return nil, fetchBodyReadError(ctx)
	}
	value, _, ok := oauthJSONValue(r.body)
	if !ok {
		return nil, oauthJSONRejection(r.body)
	}
	if value == nil {
		return nil, fmt.Errorf("Cannot read properties of null (reading '%s')", member)
	}
	members, _ := value.(map[string]any)
	return members, nil
}

func (o *RadiusOAuth) loadOAuthDiscovery(ctx context.Context) (string, error) {
	reply, err := o.send(ctx, http.MethodGet, "/v1/oauth", nil)
	if err != nil {
		return "", err
	}
	if !reply.ok() {
		// `${await response.text()}` is not caught either.
		if reply.readErr != nil {
			return "", fetchBodyReadError(ctx)
		}
		return "", fmt.Errorf("Could not load Radius OAuth config from %s: %d %s", o.gateway, reply.status, fetchBodyText(reply.body))
	}
	discovery, err := reply.json(ctx, "authorizationEndpoint")
	if err != nil {
		return "", err
	}
	endpoint, ok := discovery["authorizationEndpoint"].(string)
	if !ok {
		return "", fmt.Errorf("Invalid Radius OAuth config from %s", o.gateway)
	}
	return endpoint, nil
}

// send issues one gateway request and reads the whole body.
func (o *RadiusOAuth) send(ctx context.Context, method, path string, form formFields) (radiusReply, error) {
	endpoint, err := resolveGatewayPath(o.gateway, path)
	if err != nil {
		return radiusReply{}, err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := newRadiusRequest(ctx, method, endpoint, body)
	if err != nil {
		return radiusReply{}, err
	}
	request.Header.Set("accept", "application/json")
	if form != nil {
		request.Header.Set("content-type", "application/x-www-form-urlencoded")
	}
	response, err := radiusHTTPClient.Do(request)
	if err != nil {
		return radiusReply{}, err
	}
	defer func() { _ = response.Body.Close() }()
	data, readErr := io.ReadAll(response.Body)
	return radiusReply{status: response.StatusCode, body: data, readErr: readErr}, nil
}

// sendForm posts form data, mapping a cancelled request to "Login cancelled".
func (o *RadiusOAuth) sendForm(ctx context.Context, path string, form formFields) (radiusReply, error) {
	reply, err := o.send(ctx, http.MethodPost, path, form)
	if err != nil && ctx.Err() != nil {
		return radiusReply{}, errors.New(radiusLoginCancelled)
	}
	return reply, err
}

// RadiusOAuthResponseError is a non-2xx OAuth response. Mirrors upstream
// OAuthResponseError.
type RadiusOAuthResponseError struct {
	Status     int
	OAuthError string
	message    string
}

func (e *RadiusOAuthResponseError) Error() string { return e.message }

// readRadiusOAuthResponseError is readOAuthResponseError: the text of the body (empty when it failed to read), whose JSON object members error and error_description name the failure. Text that is not JSON, or is null, is the description itself; any other JSON value names nothing.
func readRadiusOAuthResponseError(reply radiusReply, message string) *RadiusOAuthResponseError {
	var oauthError, description string
	if reply.readErr == nil {
		if text := fetchBodyText(reply.body); text != "" {
			if value, _, ok := oauthJSONValue(reply.body); !ok || value == nil {
				description = text
			} else if data, isObject := value.(map[string]any); isObject {
				oauthError, _ = data["error"].(string)
				description, _ = data["error_description"].(string)
			}
		}
	}
	detail := description
	switch {
	case oauthError != "" && description != "":
		detail = oauthError + ": " + description
	case oauthError != "":
		detail = oauthError
	case description == "":
		detail = fmt.Sprint(reply.status)
	}
	return &RadiusOAuthResponseError{Status: reply.status, OAuthError: oauthError, message: message + ": " + detail}
}

func (o *RadiusOAuth) requestOAuthToken(ctx context.Context, form formFields) (OAuthCredentials, error) {
	reply, err := o.sendForm(ctx, "/v1/oauth/token", form)
	if err != nil {
		return OAuthCredentials{}, err
	}
	if !reply.ok() {
		return OAuthCredentials{}, readRadiusOAuthResponseError(reply, "Radius OAuth token request failed")
	}
	data, err := reply.json(ctx, "access_token")
	if err != nil {
		return OAuthCredentials{}, err
	}
	// Pi builds the credentials from whatever members the body has. An absent or null token is the empty string here, so a gateway that issues no refresh token still signs in; a token of another JSON type cannot be represented, so such a body is an error.
	access, accessOK := radiusTokenMember(data, "access_token")
	refresh, refreshOK := radiusTokenMember(data, "refresh_token")
	if !accessOK || !refreshOK {
		return OAuthCredentials{}, errors.New("Radius OAuth token response has a token that is not a string")
	}
	scope, _ := data["scope"].(string)
	// `Date.now() + data.expires_in * 1000`: a missing member is undefined, so NaN, and null multiplies as 0.
	expiresIn := math.NaN()
	if raw, present := data["expires_in"]; present {
		switch number := raw.(type) {
		case json.Number:
			expiresIn = jsnumber.FromJSON([]byte(number))
		case nil:
			expiresIn = 0
		}
	}
	creds := OAuthCredentials{Access: access, Refresh: refresh, Scope: scope, order: credentialKeyOrder{keys: radiusCredentialMembers}}
	creds.SetExpiresMillis(float64(o.now().UnixMilli()) + float64(expiresIn*1000) - float64(radiusTokenExpirySkew.Milliseconds()))
	return creds, nil
}

func (o *RadiusOAuth) loginWithBrowser(ctx context.Context, authorizationEndpoint string, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return OAuthCredentials{}, err
	}
	state := uuid.NewString()
	authorizeURL, err := radiusAuthorizeURL(authorizationEndpoint, orderedQuery(
		"response_type", "code", "client_id", radiusOAuthClientID, "redirect_uri", o.redirectURI(),
		"scope", radiusOAuthScope, "code_challenge", pkce.Challenge, "code_challenge_method", "S256",
		"handoff", "url", "state", state,
	))
	if err != nil {
		return OAuthCredentials{}, err
	}
	host, port, err := net.SplitHostPort(o.callbackAddress)
	if err != nil {
		return OAuthCredentials{}, err
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return OAuthCredentials{}, err
	}
	callback, err := StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[OAuthCredentials]{
		ProviderName: "Radius",
		Host:         host,
		Port:         portNumber,
		Path:         radiusCallbackPath,
		State:        &state,
		Complete: func(ctx context.Context, code string) (OAuthCredentials, error) {
			return o.requestOAuthToken(ctx, formFields{
				{"grant_type", "authorization_code"},
				{"client_id", radiusOAuthClientID},
				{"redirect_uri", o.redirectURI()},
				{"code", code},
				{"code_verifier", pkce.Verifier},
			})
		},
	})
	if err != nil {
		return OAuthCredentials{}, err
	}
	defer callback.Close()
	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Listening for OAuth callback on " + o.redirectURI())
	}
	if callbacks.OnAuth != nil {
		callbacks.OnAuth(OAuthAuthInfo{URL: authorizeURL, Instructions: "Continue in your browser."})
	}
	credentials, ok, err := callback.Wait()
	if err != nil {
		return OAuthCredentials{}, err
	}
	if !ok {
		return OAuthCredentials{}, errors.New("OAuth callback did not complete.")
	}
	return credentials, nil
}

// radiusAuthorizeURL is `const url = new URL(endpoint); url.search = query; url.toString()` (radius.ts loginWithBrowser).
func radiusAuthorizeURL(endpoint, query string) (string, error) {
	authorizeURL, err := nodeurl.ParseURL(endpoint)
	if err != nil {
		return "", err
	}
	return authorizeURL.WithSearch(query), nil
}

// orderedQuery is URLSearchParams.toString for a flat name, value, name, value list; see formFields.
func orderedQuery(pairs ...string) string {
	fields := make(formFields, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		fields = append(fields, [2]string{pairs[i], pairs[i+1]})
	}
	return fields.Encode()
}

func (o *RadiusOAuth) requestDeviceAuthorization(ctx context.Context) (radiusDeviceAuthorization, error) {
	reply, err := o.sendForm(ctx, "/v1/oauth/device", formFields{{"client_id", radiusOAuthClientID}, {"scope", radiusOAuthScope}})
	if err != nil {
		return radiusDeviceAuthorization{}, err
	}
	if !reply.ok() {
		return radiusDeviceAuthorization{}, readRadiusOAuthResponseError(reply, "Radius OAuth device authorization failed")
	}
	data, err := reply.json(ctx, "device_code")
	if err != nil {
		return radiusDeviceAuthorization{}, err
	}
	// Pi accepts any truthy member; a string field holds only a string, and expires_in only a number.
	device := radiusDeviceAuthorization{}
	device.DeviceCode, _ = data["device_code"].(string)
	device.UserCode, _ = data["user_code"].(string)
	device.VerificationURI, _ = data["verification_uri"].(string)
	if number, ok := data["expires_in"].(json.Number); ok {
		device.ExpiresIn = jsnumber.FromJSON([]byte(number))
	}
	if number, ok := data["interval"].(json.Number); ok {
		interval := jsnumber.FromJSON([]byte(number))
		device.Interval = &interval
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
	form := formFields{{"grant_type", radiusDeviceCodeGrantType}, {"client_id", radiusOAuthClientID}, {"device_code", device.DeviceCode}}
	return PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[OAuthCredentials]{
		IntervalSeconds:  device.Interval,
		ExpiresInSeconds: &device.ExpiresIn,
		Poll:             func() (DeviceCodePollResult[OAuthCredentials], error) { return o.pollDeviceToken(ctx, form) },
	})
}

// radiusTokenMember is a token member of a token response: its string, or "" when it is absent or null. ok is false for any other JSON type.
func radiusTokenMember(data map[string]any, key string) (token string, ok bool) {
	switch value := data[key].(type) {
	case string:
		return value, true
	case nil:
		return "", true
	}
	return "", false
}

// pollDeviceToken is one poll of the token endpoint (the poll callback of loginWithDeviceCode).
func (o *RadiusOAuth) pollDeviceToken(ctx context.Context, form formFields) (DeviceCodePollResult[OAuthCredentials], error) {
	credentials, err := o.requestOAuthToken(ctx, form)
	if err == nil {
		return DeviceCodePollResult[OAuthCredentials]{Status: DevicePollComplete, Value: credentials}, nil
	}
	return radiusDevicePollResult(err)
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
