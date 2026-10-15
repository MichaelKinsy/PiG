package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

const (
	chatgptTokenURL      = "https://auth.openai.com/api/accounts/oauth/token"
	chatgptRequiredScope = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	chatgptDeviceID      = "e61bbe28-07ef-466d-8e5d-a344f94ab305"
)

type chatgptTokenEndpoint struct {
	calls  int
	bodies []url.Values
}

// stubChatGPTTokenEndpoint replaces the process HTTP client for the test, as the other OAuth flow tests do.
func stubChatGPTTokenEndpoint(t *testing.T, response map[string]any) *chatgptTokenEndpoint {
	t.Helper()
	endpoint := &chatgptTokenEndpoint{}
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: clsFetch(func(request *http.Request) (*http.Response, error) {
		endpoint.calls++
		if request.URL.String() != chatgptTokenURL {
			t.Errorf("token url=%s", request.URL)
		}
		body, _ := io.ReadAll(request.Body)
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Error(err)
		}
		endpoint.bodies = append(endpoint.bodies, values)
		data, _ := json.Marshal(response)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	return endpoint
}

func chatgptTokenResponse(scope string) map[string]any {
	return map[string]any{"access_token": "access-token", "refresh_token": "refresh-token", "expires_in": 3600, "id_token": "id-token", "scope": scope}
}

// chatgptLoginInteraction answers the manual_code prompt with a pasted redirect URL built from the authorization URL.
func chatgptLoginInteraction(callbackClientID string, onAuthorize func(*url.URL)) AuthInteraction {
	var authorizeURL *url.URL
	return AuthInteraction{
		Notify: func(event AuthEvent) {
			if auth, ok := event.(AuthURLEvent); ok {
				authorizeURL, _ = url.Parse(auth.URL)
				if onAuthorize != nil {
					onAuthorize(authorizeURL)
				}
			}
		},
		Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
			if prompt.Type() != "manual_code" {
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type())
			}
			if authorizeURL == nil {
				return "", errors.New("Authorization URL was not emitted before the callback prompt")
			}
			callback, err := url.Parse(authorizeURL.Query().Get("redirect_uri"))
			if err != nil {
				return "", err
			}
			query := callback.Query()
			query.Set("code", "authorization-code")
			query.Set("state", authorizeURL.Query().Get("state"))
			if callbackClientID != "" {
				query.Set("client_id", callbackClientID)
			}
			callback.RawQuery = query.Encode()
			return callback.String(), nil
		},
	}
}

func chatgptConnectedCredential() Credential {
	scopes, _ := json.Marshal(strings.Split(chatgptRequiredScope, " "))
	return Credential{Type: CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: 0, Extra: map[string]json.RawMessage{"clientId": json.RawMessage(`"oaiapp_existing"`), "scopes": scopes}}
}

func chatgptExtra(t *testing.T, credential Credential) (clientID string, scopes []string) {
	t.Helper()
	if err := json.Unmarshal(credential.Extra["clientId"], &clientID); err != nil {
		t.Fatalf("clientId: %v (%s)", err, credential.Extra["clientId"])
	}
	if err := json.Unmarshal(credential.Extra["scopes"], &scopes); err != nil {
		t.Fatalf("scopes: %v (%s)", err, credential.Extra["scopes"])
	}
	return clientID, scopes
}

func chatgptOAuth(t *testing.T) *OAuthAuth {
	t.Helper()
	// Listen on a per-process loopback address so concurrent test processes do not contend for the fixed callback port.
	isolateAnthropicCallbackHost(t)
	method, ok := OAuthProviderAuth("openai")
	if !ok || method == nil {
		t.Fatal("openai has no OAuth method")
	}
	return method
}

// Ports packages/ai/test/openai-chatgpt-oauth.test.ts.
func TestOpenAIChatGPTOAuthUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/openai-chatgpt-oauth.test.ts:74
	t.Run("registers a user-owned client and stores its issued ID and granted scopes", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		var authorizeURL *url.URL
		endpoint := stubChatGPTTokenEndpoint(t, chatgptTokenResponse(chatgptRequiredScope))

		credential, err := oauth.Login(t.Context(), chatgptLoginInteraction("oaiapp_issued", func(u *url.URL) { authorizeURL = u }), LoginOptions{GetDeviceID: func() string { return chatgptDeviceID }})
		if err != nil {
			t.Fatal(err)
		}

		query := authorizeURL.Query()
		// D26: Pi sends agent_name_hint "Pi"; PiG sends its own name.
		for name, want := range map[string]string{"client_id": "dynamic_agent_client", "agent_name_hint": pigidentity.ChatGPTAgentName, "ext_agent_host_id": "urn:uuid:" + chatgptDeviceID, "scope": chatgptRequiredScope, "redirect_uri": "http://127.0.0.1:1455/auth/callback", "resource": "https://api.openai.com/v1", "code_challenge_method": "S256"} {
			if got := query.Get(name); got != want {
				t.Errorf("%s=%q want %q", name, got, want)
			}
		}
		if len(endpoint.bodies) != 1 {
			t.Fatalf("token requests=%d", len(endpoint.bodies))
		}
		exchange := endpoint.bodies[0]
		if exchange.Get("client_id") != "oaiapp_issued" || exchange.Get("code") != "authorization-code" || exchange.Get("resource") != "https://api.openai.com/v1" || exchange.Get("code_verifier") == "" {
			t.Errorf("exchange=%v", exchange)
		}
		clientID, scopes := chatgptExtra(t, credential)
		if credential.Type != CredentialOAuth || credential.Access != "access-token" || credential.Refresh != "refresh-token" || clientID != "oaiapp_issued" || !reflect.DeepEqual(scopes, strings.Split(chatgptRequiredScope, " ")) {
			t.Errorf("credential=%+v", credential)
		}
	})
	// .upstream/v1.1.0/packages/ai/test/openai-chatgpt-oauth.test.ts:111
	t.Run("uses the app's agent name as the name hint", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		var authorizeURL *url.URL
		stubChatGPTTokenEndpoint(t, chatgptTokenResponse(chatgptRequiredScope))
		agentName := "my-app"

		if _, err := oauth.Login(t.Context(), chatgptLoginInteraction("oaiapp_issued", func(u *url.URL) { authorizeURL = u }), LoginOptions{GetDeviceID: func() string { return chatgptDeviceID }, AgentName: &agentName}); err != nil {
			t.Fatal(err)
		}
		if got := authorizeURL.Query().Get("agent_name_hint"); got != "my-app" {
			t.Fatalf("agent_name_hint = %q, want my-app", got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/openai-chatgpt-oauth.test.ts:111
	t.Run("rejects registration without an issued client ID", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		endpoint := stubChatGPTTokenEndpoint(t, chatgptTokenResponse(chatgptRequiredScope))

		_, err := oauth.Login(t.Context(), chatgptLoginInteraction("", nil), LoginOptions{GetDeviceID: func() string { return chatgptDeviceID }})

		if err == nil || !strings.Contains(err.Error(), "registration callback did not contain an issued client ID") {
			t.Errorf("err=%v", err)
		}
		if endpoint.calls != 0 {
			t.Error("token endpoint was called")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/openai-chatgpt-oauth.test.ts:120
	t.Run("rejects a token response that did not grant direct token use", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		stubChatGPTTokenEndpoint(t, chatgptTokenResponse("openid profile email offline_access resource.invoke"))

		_, err := oauth.Login(t.Context(), chatgptLoginInteraction("oaiapp_issued", nil), LoginOptions{GetDeviceID: func() string { return chatgptDeviceID }})

		if err == nil || !strings.Contains(err.Error(), "grant did not include chatgpt.tokens.use.direct") {
			t.Errorf("err=%v", err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/openai-chatgpt-oauth.test.ts:130
	t.Run("requires a device ID before starting authorization", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		authorizationStarted := false
		interaction := chatgptLoginInteraction("", func(*url.URL) { authorizationStarted = true })

		_, err := oauth.Login(t.Context(), interaction, LoginOptions{})
		if err == nil || !strings.Contains(err.Error(), "requires a device ID") {
			t.Errorf("without options: err=%v", err)
		}
		_, err = oauth.Login(t.Context(), interaction, LoginOptions{GetDeviceID: func() string { return "not-a-uuid" }})
		if err == nil || !strings.Contains(err.Error(), "requires a device ID") {
			t.Errorf("invalid ID: err=%v", err)
		}
		if authorizationStarted {
			t.Error("authorization started")
		}
	})
	// .upstream/v0.99.1/packages/ai/test/openai-chatgpt-oauth.test.ts:145
	t.Run("requires refresh responses to rotate the refresh token", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		response := chatgptTokenResponse(chatgptRequiredScope)
		delete(response, "refresh_token")
		stubChatGPTTokenEndpoint(t, response)

		_, err := oauth.Refresh(t.Context(), chatgptConnectedCredential())

		if err == nil || !strings.Contains(err.Error(), "token response has invalid refresh_token") {
			t.Errorf("err=%v", err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/openai-chatgpt-oauth.test.ts:154
	t.Run("refreshes with the credential's issued client ID and stores replacement scopes", func(t *testing.T) {
		oauth := chatgptOAuth(t)
		response := chatgptTokenResponse(chatgptRequiredScope)
		response["access_token"], response["refresh_token"] = "new-access", "new-refresh"
		endpoint := stubChatGPTTokenEndpoint(t, response)

		before := time.Now().UnixMilli()
		credential, err := oauth.Refresh(t.Context(), chatgptConnectedCredential())
		if err != nil {
			t.Fatal(err)
		}

		// expires_in is 3600 seconds; the credential expires 3 minutes early so it is refreshed in time.
		if credential.ExpiresMillis() < float64(before+(3600-180)*1000) || credential.ExpiresMillis() > float64(time.Now().UnixMilli()+(3600-180)*1000) {
			t.Errorf("expires=%v before=%d", credential.ExpiresMillis(), before)
		}
		refresh := endpoint.bodies[0]
		if refresh.Get("grant_type") != "refresh_token" || refresh.Get("client_id") != "oaiapp_existing" || refresh.Get("refresh_token") != "old-refresh" || refresh.Get("resource") != "https://api.openai.com/v1" || refresh.Has("scope") {
			t.Errorf("refresh=%v", refresh)
		}
		clientID, scopes := chatgptExtra(t, credential)
		if credential.Access != "new-access" || credential.Refresh != "new-refresh" || clientID != "oaiapp_existing" || !reflect.DeepEqual(scopes, strings.Split(chatgptRequiredScope, " ")) {
			t.Errorf("credential=%+v", credential)
		}
	})
}
