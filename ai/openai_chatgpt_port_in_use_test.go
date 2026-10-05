package ai

import (
	"net"
	"net/url"
	"strconv"
	"testing"
)

// Sign in with ChatGPT fails with a port-in-use error instead of continuing
// without its callback server when another login holds the callback port
// (#10265). Continuing let the other listener reject the browser's callback as
// an OAuth state mismatch.
// upstream: packages/ai/src/auth/oauth/openai-chatgpt.ts:241-248 (1.0.1).
func TestOpenAIChatGPTLoginFailsWhenTheCallbackPortIsInUse(t *testing.T) {
	oauth := chatgptOAuth(t)
	holder, err := net.Listen("tcp", net.JoinHostPort(oauthCallbackHost(), strconv.Itoa(chatgptCallbackPort)))
	if err != nil {
		t.Fatalf("cannot occupy the ChatGPT callback port on %s: %v", oauthCallbackHost(), err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	endpoint := stubChatGPTTokenEndpoint(t, chatgptTokenResponse(chatgptRequiredScope))
	authorized := false

	_, err = oauth.Login(t.Context(), chatgptLoginInteraction("oaiapp_issued", func(*url.URL) { authorized = true }), LoginOptions{GetDeviceID: func() string { return chatgptDeviceID }})

	const want = "Port 1455 is in use, probably by an unfinished login in another pi session or by the Codex CLI. Cancel that login and try again."
	if err == nil || err.Error() != want {
		t.Fatalf("Login error = %v, want %q", err, want)
	}
	if authorized {
		t.Error("the authorization URL was emitted although the callback server could not start")
	}
	if len(endpoint.bodies) != 0 {
		t.Errorf("token requests = %d, want none", len(endpoint.bodies))
	}
}
