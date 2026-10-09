package ai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// openai-chatgpt.ts credentialFromTokenResponse splits the scope with scope.trim().split(/\s+/): JavaScript's \s
// excludes U+0085, so "a\u0085b" is one scope, and includes U+FEFF.
func TestChatGPTTokenScopesSplitOnJavaScriptWhitespace(t *testing.T) {
	body := func(scope string) chatgptTokenBody {
		return chatgptTokenBody{
			"access_token": json.RawMessage(`"a"`), "refresh_token": json.RawMessage(`"r"`), "expires_in": json.RawMessage(`60`),
			"scope": json.RawMessage(strconv.Quote(scope)),
		}
	}
	credentials, err := chatgptCredentialFromTokenResponse(body("x\u0085y \ufefffoo\u2028"+chatgptDirectTokenScope), "client")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(credentials.Extra["scopes"]); got != "[\"x\u0085y\",\"foo\",\""+chatgptDirectTokenScope+"\"]" {
		t.Fatalf("scopes = %s", got)
	}
	if _, err := chatgptCredentialFromTokenResponse(body("a\u0085"+chatgptDirectTokenScope), "client"); err == nil {
		t.Fatal("a scope joined to the direct-token scope by U+0085 must not satisfy the grant check")
	}
}

// openai-chatgpt.ts sendHtml sets only Content-Type (no cache-control), and request.url is parsed with
// new URL(request.url, REDIRECT_URI): the pathname stays percent-encoded and "%2e" segments resolve.
func TestChatGPTCallbackServerResponseMatchesSendHtml(t *testing.T) {
	server, err := startChatGPTCallbackServer("state")
	if err != nil {
		t.Skipf("callback port unavailable: %v", err)
	}
	t.Cleanup(server.close)
	get := func(target string) (string, http.Header) {
		conn, err := net.Dial("tcp", net.JoinHostPort(oauthCallbackHost(), strconv.Itoa(chatgptCallbackPort)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_, _ = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n", target)
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.Status, response.Header
	}
	status, header := get("/auth/%63allback")
	if !strings.HasPrefix(status, "404") {
		t.Fatalf("encoded path status = %s, want 404", status)
	}
	if got := header.Get("Cache-Control"); got != "" {
		t.Fatalf("cache-control = %q, want none", got)
	}
	if got := header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q", got)
	}
	// A "%2e" dot segment resolves, so the request reaches the callback route and fails on its missing code.
	if status, _ := get("/auth/x/%2e%2e/callback"); !strings.HasPrefix(status, "400") {
		t.Fatalf("dot-segment path status = %s, want 400", status)
	}
}
