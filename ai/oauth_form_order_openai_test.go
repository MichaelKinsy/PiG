package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// recordDefaultClientBodies answers every http.DefaultClient request with answer and keeps the raw bodies.
func recordDefaultClientBodies(t *testing.T, answer string) func() []string {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(answer)), Request: request}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

// The OpenAI Codex, Sign in with ChatGPT and GitHub Copilot token requests are the remaining OAuth form bodies Pi builds with
// `new URLSearchParams({...})`: openai-codex.ts:154-160,173-177, openai-chatgpt.ts:190-197,214-219, github-copilot.ts:215-218,282-286.
// The fields keep their insertion order and the WHATWG serializer keeps `*` and encodes `~`; url.Values.Encode sorted the keys.
func TestOpenAIAndCopilotOAuthFormBodiesKeepPiFieldOrderAndEncoding(t *testing.T) {
	const refresh = "r*~ x"
	const encoded = "r*%7E+x"
	t.Run("codex exchange and refresh", func(t *testing.T) {
		bodies := recordDefaultClientBodies(t, `{"access_token":"a.b.c","refresh_token":"b","expires_in":3600}`)
		_, _ = exchangeCodexAuthorizationCode(t.Context(), "c*~", "v~", "http://localhost:1455/auth/callback")
		_, _ = RefreshCodexToken(t.Context(), refresh)
		want := []string{
			"grant_type=authorization_code&client_id=" + codexClientID + "&code=c*%7E&code_verifier=v%7E&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback",
			"grant_type=refresh_token&refresh_token=" + encoded + "&client_id=" + codexClientID,
		}
		if got := bodies(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("bodies =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("chatgpt exchange and refresh", func(t *testing.T) {
		bodies := recordDefaultClientBodies(t, `{"access_token":"a","refresh_token":"b","id_token":"i","expires_in":3600}`)
		_, _ = exchangeChatGPTAuthorizationCode(t.Context(), "c*~", "v~", "client~1")
		clientID, _ := json.Marshal("client~1")
		_, _ = RefreshOpenAIChatGPTToken(t.Context(), OAuthCredentials{Refresh: refresh, Extra: map[string]json.RawMessage{"clientId": clientID}})
		redirect := strings.NewReplacer(":", "%3A", "/", "%2F").Replace(chatgptRedirectURI)
		resource := strings.NewReplacer(":", "%3A", "/", "%2F").Replace(chatgptResource)
		want := []string{
			"grant_type=authorization_code&client_id=client%7E1&code=c*%7E&code_verifier=v%7E&redirect_uri=" + redirect + "&resource=" + resource,
			"grant_type=refresh_token&client_id=client%7E1&refresh_token=" + encoded + "&resource=" + resource,
		}
		if got := bodies(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("bodies =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("copilot device code and poll", func(t *testing.T) {
		bodies := recordDefaultClientBodies(t, `{"device_code":"d","user_code":"U","verification_uri":"https://github.com/login/device","expires_in":60,"access_token":"tok"}`)
		if _, err := startDeviceFlow(t.Context(), "github.com"); err != nil {
			t.Fatal(err)
		}
		zero := float64(0)
		_, _ = pollForGitHubAccessToken(t.Context(), "github.com", &deviceCodeResponse{DeviceCode: "d*~", ExpiresIn: 60, Interval: &zero})
		want := []string{
			"client_id=" + copilotClientID + "&scope=read%3Auser",
			"client_id=" + copilotClientID + "&device_code=d*%7E&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code",
		}
		if got := bodies(); len(got) < 2 || strings.Join(got[:2], "\n") != strings.Join(want, "\n") {
			t.Fatalf("bodies =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}
