//go:build linux || darwin

package uiviewweb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/browsertest"
)

// The timer that replaces a code nobody used does not keep the process alive (it is not among the resources Node counts as active).
// It needs bridge.state, which connect_test.go defines for linux and darwin only.
func TestBridgeReplacesACodeNobodyUsedHoldsNoTimer(t *testing.T) {
	idle := runBridge(t, nodeBinary(t), nil)
	if n := idle.state(t).timers(); n != 0 {
		t.Errorf("an idle bridge holds %d timers, want none: the code's timer must not keep the process alive", n)
	}
}

// What the browser holds for the bridge is not sent to another port of the host. A server of another user on 127.0.0.1
// gets a page visit from the same browser: it is sent no cookie, no Authorization and no code or secret, and a request
// its page makes to the bridge carries none either (and is refused for its Origin).
func TestSecretNeverLeavesThePagesPort(t *testing.T) {
	log := filepath.Join(t.TempDir(), "requests.jsonl")
	b, _ := startHooked(t, []string{"HOOK_REQUEST_LOG=" + log}, demoArgs(t)...)
	var mu sync.Mutex
	var other []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		other = append(other, req.Method+" "+req.URL.String()+"\n"+fmt.Sprint(req.Header))
		mu.Unlock()
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		_, _ = fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><img src="/pixel.png"><script>
fetch(%q, { method: "POST", mode: "no-cors", credentials: "include", headers: { "Content-Type": "text/plain" }, body: "{}" }).catch(() => {});
</script>`, b.base+"/send?session=x")
	}))
	t.Cleanup(server.Close)
	browser := browsertest.Start(t)
	code := b.code(t)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	secret := pageSecret(t, browser)
	// The same tab goes to the other server, as a user's does between two local dev pages.
	browser.Open(server.URL + "/")
	eventually(t, "the other server's page to reach the bridge", func() bool {
		for _, entry := range readRequestLog(t, log) {
			if entry.header("origin") == server.URL && strings.HasPrefix(entry.Line, "POST /send") {
				return true
			}
		}
		return false
	})
	eventually(t, "the other server's picture", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, entry := range other {
			if strings.HasPrefix(entry, "GET /pixel.png") {
				return true
			}
		}
		return false
	})
	mu.Lock()
	for _, entry := range other {
		for _, held := range []string{code, secret} {
			holdsNoSecret(t, "a request to the other server: "+entry, entry, held)
		}
		if strings.Contains(strings.ToLower(entry), "cookie") || strings.Contains(strings.ToLower(entry), "authorization") {
			t.Errorf("a request to the other server carries a cookie or an Authorization:\n%s", entry)
		}
	}
	mu.Unlock()
	for _, entry := range readRequestLog(t, log) {
		if entry.header("origin") == server.URL && (entry.Headers["cookie"] != nil || entry.Headers["authorization"] != nil) {
			t.Errorf("the other page's request to the bridge carries credentials:\n%s", entry)
		}
	}
}
