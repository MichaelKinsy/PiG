package ai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type radiusGatewayRecorder struct {
	mu    sync.Mutex
	paths []string
	forms []url.Values
}

func (recorder *radiusGatewayRecorder) record(t *testing.T, request *http.Request) url.Values {
	t.Helper()
	if err := request.ParseForm(); err != nil {
		t.Errorf("parse form: %v", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.paths = append(recorder.paths, request.URL.Path)
	recorder.forms = append(recorder.forms, request.PostForm)
	return request.PostForm
}

func writeRadiusJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("content-type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func newTestRadiusOAuth(gateway string, now time.Time) *RadiusOAuth {
	oauth := CreateRadiusOAuth(RadiusOAuthOptions{ID: "radius", Name: "Radius", Gateway: gateway})
	oauth.now = func() time.Time { return now }
	return oauth
}

func selectRadiusMethod(method string) func(OAuthSelectPrompt) (string, error) {
	return func(prompt OAuthSelectPrompt) (string, error) {
		if prompt.Message != "Sign in to Radius:" || len(prompt.Options) != 2 || prompt.Options[0].ID != RadiusLoginMethodBrowser || prompt.Options[1].ID != RadiusLoginMethodDeviceCode {
			return "", &RadiusOAuthResponseError{message: "unexpected prompt"}
		}
		return method, nil
	}
}

// Ported from radius-oauth.test.ts "uses gateway endpoints directly for device login".
func TestRadiusOAuthDeviceLoginUsesGatewayEndpoints(t *testing.T) {
	recorder := &radiusGatewayRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		form := recorder.record(t, request)
		switch request.URL.Path {
		case "/v1/oauth/device":
			if form.Get("client_id") != "pi-gateway" || form.Get("scope") != "gateway offline_access" {
				t.Errorf("device form = %v", form)
			}
			writeRadiusJSON(writer, 200, map[string]any{"device_code": "device-code", "user_code": "ABCD-1234", "verification_uri": "https://radius-ui.example/pair", "expires_in": 600, "interval": 5})
		case "/v1/oauth/token":
			if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || form.Get("client_id") != "pi-gateway" || form.Get("device_code") != "device-code" {
				t.Errorf("token form = %v", form)
			}
			writeRadiusJSON(writer, 200, map[string]any{"access_token": "access-token", "refresh_token": "refresh-token", "expires_in": 3600, "scope": "gateway offline_access"})
		default:
			t.Errorf("unexpected request: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	var events []OAuthDeviceCodeInfo

	credentials, err := newTestRadiusOAuth(server.URL, now).LoginContext(context.Background(), OAuthLoginCallbacks{
		OnSelect:     selectRadiusMethod(RadiusLoginMethodDeviceCode),
		OnDeviceCode: func(info OAuthDeviceCodeInfo) { events = append(events, info) },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := OAuthCredentials{Access: "access-token", Refresh: "refresh-token", Expires: now.UnixMilli() + 3600*1000 - 60_000, Scope: "gateway offline_access"}
	if credentials != want {
		t.Fatalf("credentials = %+v, want %+v", credentials, want)
	}
	if len(events) != 1 || events[0] != (OAuthDeviceCodeInfo{UserCode: "ABCD-1234", VerificationURI: "https://radius-ui.example/pair", IntervalSeconds: 5, ExpiresInSeconds: 600}) {
		t.Fatalf("device events = %+v", events)
	}
	if !slices.Equal(recorder.paths, []string{"/v1/oauth/device", "/v1/oauth/token"}) {
		t.Fatalf("requests = %v", recorder.paths)
	}
}

// Ported from radius-oauth.test.ts "refreshes directly through the gateway without discovery".
func TestRadiusOAuthRefreshesThroughGateway(t *testing.T) {
	recorder := &radiusGatewayRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		form := recorder.record(t, request)
		if request.URL.Path != "/v1/oauth/token" || form.Get("grant_type") != "refresh_token" || form.Get("client_id") != "pi-gateway" || form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh request %s %v", request.URL.Path, form)
		}
		writeRadiusJSON(writer, 200, map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600})
	}))
	defer server.Close()

	refreshed, err := newTestRadiusOAuth(server.URL, time.Now()).RefreshToken(OAuthCredentials{Access: "old-access", Refresh: "old-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Access != "new-access" || refreshed.Refresh != "new-refresh" || len(recorder.paths) != 1 {
		t.Fatalf("refreshed = %+v, requests = %v", refreshed, recorder.paths)
	}
}

// Ported from radius-oauth.test.ts "discovers only the interactive browser authorization endpoint".
func TestRadiusOAuthBrowserDiscoveryRequiresAuthorizationEndpoint(t *testing.T) {
	recorder := &radiusGatewayRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder.record(t, request)
		writeRadiusJSON(writer, 200, map[string]any{"issuer": "https://radius-ui.example"})
	}))
	defer server.Close()

	_, err := newTestRadiusOAuth(server.URL, time.Now()).LoginContext(context.Background(), OAuthLoginCallbacks{OnSelect: selectRadiusMethod(RadiusLoginMethodBrowser)})
	if err == nil || err.Error() != "Invalid Radius OAuth config from "+server.URL {
		t.Fatalf("error = %v", err)
	}
	if !slices.Equal(recorder.paths, []string{"/v1/oauth"}) {
		t.Fatalf("requests = %v", recorder.paths)
	}
}

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func TestRadiusOAuthBrowserLoginExchangesCallbackCode(t *testing.T) {
	var verifier string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		switch request.URL.Path {
		case "/v1/oauth":
			writeRadiusJSON(writer, 200, map[string]any{"authorizationEndpoint": "https://radius-ui.example/authorize?stale=1"})
		case "/v1/oauth/token":
			verifier = request.PostForm.Get("code_verifier")
			if request.PostForm.Get("grant_type") != "authorization_code" || request.PostForm.Get("code") != "the-code" {
				t.Errorf("token form = %v", request.PostForm)
			}
			writeRadiusJSON(writer, 200, map[string]any{"access_token": "browser-access", "refresh_token": "browser-refresh", "expires_in": 60})
		}
	}))
	defer server.Close()
	oauth := newTestRadiusOAuth(server.URL, time.Now())
	oauth.callbackAddress = freeLoopbackAddress(t)
	var challenge string
	var progress []string

	credentials, err := oauth.LoginContext(context.Background(), OAuthLoginCallbacks{
		OnSelect:   selectRadiusMethod(RadiusLoginMethodBrowser),
		OnProgress: func(message string) { progress = append(progress, message) },
		OnAuth: func(info OAuthAuthInfo) {
			authorize, err := url.Parse(info.URL)
			if err != nil || info.Instructions != "Continue in your browser." {
				t.Errorf("auth info = %+v (%v)", info, err)
				return
			}
			query := authorize.Query()
			challenge = query.Get("code_challenge")
			if query.Has("stale") || query.Get("client_id") != "pi-gateway" || query.Get("handoff") != "url" || query.Get("code_challenge_method") != "S256" || query.Get("scope") != "gateway offline_access" {
				t.Errorf("authorize query = %v", query)
			}
			if !strings.HasPrefix(authorize.RawQuery, "response_type=code&client_id=pi-gateway&redirect_uri=") {
				t.Errorf("authorize parameter order = %s", authorize.RawQuery)
			}
			go func() {
				wrong, err := http.Get(query.Get("redirect_uri") + "?code=x&state=wrong")
				if err == nil {
					_ = wrong.Body.Close()
				}
				response, err := http.Get(query.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(query.Get("state")))
				if err != nil {
					t.Errorf("callback: %v", err)
					return
				}
				_ = response.Body.Close()
			}()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(verifier))
	if credentials.Access != "browser-access" || base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
		t.Fatalf("credentials = %+v, verifier/challenge mismatch", credentials)
	}
	if len(progress) != 1 || progress[0] != "Listening for OAuth callback on http://"+oauth.callbackAddress+"/oauth/callback" {
		t.Fatalf("progress = %v", progress)
	}
}

func TestRadiusOAuthBrowserLoginCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeRadiusJSON(writer, 200, map[string]any{"authorizationEndpoint": "https://radius-ui.example/authorize"})
	}))
	defer server.Close()
	oauth := newTestRadiusOAuth(server.URL, time.Now())
	oauth.callbackAddress = freeLoopbackAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := oauth.LoginContext(ctx, OAuthLoginCallbacks{
		OnSelect: selectRadiusMethod(RadiusLoginMethodBrowser),
		OnAuth:   func(OAuthAuthInfo) { cancel() },
	})
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("error = %v", err)
	}
}

func TestRadiusOAuthDevicePollingMapsOAuthErrors(t *testing.T) {
	for oauthError, want := range map[string]string{
		"expired_token": "Device authorization expired.",
		"access_denied": "Device authorization was denied.",
		"server_error":  "Radius OAuth token request failed: server_error: boom",
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/v1/oauth/device" {
				writeRadiusJSON(writer, 200, map[string]any{"device_code": "d", "user_code": "u", "verification_uri": "https://v", "expires_in": 60})
				return
			}
			writeRadiusJSON(writer, 400, map[string]any{"error": oauthError, "error_description": "boom"})
		}))
		_, err := newTestRadiusOAuth(server.URL, time.Now()).LoginContext(context.Background(), OAuthLoginCallbacks{OnSelect: selectRadiusMethod(RadiusLoginMethodDeviceCode)})
		server.Close()
		if err == nil || err.Error() != want {
			t.Errorf("%s: error = %v, want %q", oauthError, err, want)
		}
	}
}

func TestRadiusOAuthIsRegisteredForTheDefaultGateway(t *testing.T) {
	provider, ok := GetOAuthProvider("radius")
	radius, isRadius := provider.(*RadiusOAuth)
	if !ok || !isRadius || radius.Name() != "Radius" || radius.Gateway() != DefaultRadiusGateway || radius.redirectURI() != "http://127.0.0.1:1456/oauth/callback" {
		t.Fatalf("radius OAuth provider = %#v, %t", provider, ok)
	}
}
