package oauth_test

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// PiG-only: packages/mcp/test/oauth.test.ts exercises the callback server's pages and extra paths but not its other
// options (host, redirectHost, port, path, timeoutMs), the code/state/iss it delivers, or the waits it rejects when
// it times out, closes, or already has the state pending.
func TestOAuthCallbackServerOptionsShapeTheRedirectURL(t *testing.T) {
	defaults, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = defaults.Close() }()
	if !strings.HasPrefix(defaults.RedirectURL, "http://127.0.0.1:") || !strings.HasSuffix(defaults.RedirectURL, "/callback") {
		t.Fatalf("default redirect = %s", defaults.RedirectURL)
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	custom, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{Host: "127.0.0.1", RedirectHost: "localhost", Port: port, Path: "/oauth/cb"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = custom.Close() }()
	if want := "http://localhost:" + strconv.Itoa(port) + "/oauth/cb"; custom.RedirectURL != want {
		t.Fatalf("redirect = %s, want %s", custom.RedirectURL, want)
	}
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/callback")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("the default path answered %d although Path is /oauth/cb", response.StatusCode)
	}

	v6, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{Host: "127.0.0.1", RedirectHost: "::1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v6.Close() }()
	if !strings.HasPrefix(v6.RedirectURL, "http://[::1]:") {
		t.Fatalf("an IPv6 redirect host must be bracketed: %s", v6.RedirectURL)
	}
}

func TestOAuthCallbackServerDeliversCodeStateAndIssToTheWait(t *testing.T) {
	server, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	wait := startWait(t, server, "state-1")
	if _, err := startWait(t, server, "state-1")(); err == nil || err.Error() != "OAuth state is already pending" {
		t.Fatalf("duplicate state err = %v", err)
	}
	response, err := http.Get(server.RedirectURL + "?code=the-code&state=state-1&iss=https%3A%2F%2Fas.example")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	got, err := wait()
	if err != nil || got != (oauth.OAuthCallback{Code: "the-code", State: "state-1", Iss: "https://as.example"}) {
		t.Fatalf("callback = %+v, %v", got, err)
	}
}

func TestOAuthCallbackServerRejectsAWaitWhenItTimesOutOrCloses(t *testing.T) {
	server, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{Timeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	timedOut := startWait(t, server, "slow")
	if _, err := timedOut(); err == nil || err.Error() != "OAuth callback timed out" {
		t.Fatalf("timeout err = %v", err)
	}
	freed := startWait(t, server, "slow")
	pending := startWait(t, server, "open")
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	// A timed-out state is free again: its second wait registered instead of failing at once with "already pending".
	if _, err := freed(); err == nil || err.Error() == "OAuth state is already pending" {
		t.Fatalf("the second wait for a timed-out state = %v", err)
	}
	if _, err := pending(); err == nil || err.Error() != "OAuth callback server closed" {
		t.Fatalf("close err = %v", err)
	}
}
