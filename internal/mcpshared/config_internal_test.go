package mcpshared

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi isLoopbackRedirectUri: an `http` URI on localhost, 127.0.0.1 or [::1] without query or fragment, parsed as `new URL(value)` does (it moved from coding-agent's core/mcp-servers.ts to packages/mcp/src/config.ts).
// mutation-checked: dropping `[::1]`, `https`, the query or the fragment from the rule, or comparing the unnormalized host, fails it.
func TestIsLoopbackRedirectURI(t *testing.T) {
	for uri, want := range map[string]bool{
		"http://localhost:8080/cb": true, "http://127.0.0.1/cb": true, "http://[::1]:9/cb": true,
		"https://localhost/cb": false, "http://example.com/cb": false, "http://localhost/cb?x=1": false,
		"http://localhost/cb#f": false, "not a url": false,
		// An empty query or fragment is empty for `url.search` and `url.hash`; the host is case-insensitive.
		"http://localhost:8080/cb?": true, "http://localhost:8080/cb#": true, "http://LocalHost/cb": true,
		// `url.hostname` is the WHATWG host: an IPv4 address in any notation is a dotted quad, an IPv6 literal is
		// compressed, and a domain keeps its trailing dot. Probe of Pi 0.99.2 isLoopbackRedirectUri: true, true, true,
		// true, false.
		"http://127.1:8080/cb": true, "http://0x7F.0.0.1/cb": true, "http://127.0.0.1./cb": true,
		"http://[0:0::1]:8080/cb": true, "http://localhost./cb": false,
		// The WHATWG parser percent-decodes the host, so Node accepts http://%6cocalhost/ as localhost; Go's url.Parse rejects it.
		"http://%6cocalhost:8080/cb": true, "http://%6cocalhost/cb?x=1": false, "http:localhost/cb": true,
		"https://%6cocalhost/cb": false, "http://exa%20mple.com/": false,
	} {
		if got := isLoopbackRedirectURI(uri); got != want {
			t.Errorf("isLoopbackRedirectURI(%q) = %v, want %v", uri, got, want)
		}
	}
}

// TestLoopbackRedirectURIAndNamespaceMatchPi compares isLoopbackRedirectUri and mcpNamespace with the pinned Pi, where both are exports of core/mcp-servers.js.
func TestLoopbackRedirectURIAndNamespaceMatchPi(t *testing.T) {
	uris := []string{"", "http://localhost:8080/cb", "http://127.0.0.1/cb", "http://[::1]/cb", "https://localhost/cb", "http://localhost/cb?x", "http://localhost/cb#", "http://localhost/cb#x", "http://LOCALHOST/cb", "http://0x7f.1/", "http://127.1/", "nope", "http://localhost:99999/", "http://user@localhost/"}
	servers := []string{"a", "a-b", "a-b-c", "-", "a_b"}
	var want struct {
		Loopback   []bool   `json:"loopback"`
		Namespaces []string `json:"namespaces"`
	}
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/mcp-servers.js");
emit({
	loopback: input.uris.map((u) => mod.isLoopbackRedirectUri(u)),
	namespaces: input.servers.map((s) => mod.mcpNamespace(s)),
});`, map[string]any{"uris": uris, "servers": servers}, &want)
	for j, uri := range uris {
		if got := isLoopbackRedirectURI(uri); got != want.Loopback[j] {
			t.Errorf("isLoopbackRedirectURI(%q) = %v, Pi %v", uri, got, want.Loopback[j])
		}
	}
	for j, server := range servers {
		if got := McpNamespace(server); got != want.Namespaces[j] {
			t.Errorf("McpNamespace(%q) = %q, Pi %q", server, got, want.Namespaces[j])
		}
	}
}
