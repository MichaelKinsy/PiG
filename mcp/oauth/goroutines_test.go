package oauth_test

import (
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// The callback server's serve goroutine, its timers and its connections end with Close, whether a browser delivered the redirect or only
// connected.
func TestOAuthCallbackServerLeavesNoGoroutineBehindAfterClose(t *testing.T) {
	baseline := testenv.BaselineGoroutines()
	callback, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(callback.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	spare, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spare.Close() }()
	_ = startWait(t, callback, "pending")
	if err := callback.Close(); err != nil {
		t.Fatal(err)
	}
	baseline.RequireEnded(t, 10*time.Second)
}
