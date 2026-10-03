package extension

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// Not upstream tests: guards for packages/coding-agent/src/core/mcp-servers.ts.

func TestMcpServerRegistryKeepsOwnershipOrderAndCopies(t *testing.T) {
	registry := NewMcpServerRegistry()
	var changes atomic.Int32
	registry.SetChangeListener(func() { changes.Add(1) })
	registry.Register(RegisteredMcpServer{Name: "b", Config: McpServerConfig{URL: "http://b.example/mcp"}, ExtensionPath: "one"})
	registry.Register(RegisteredMcpServer{Name: "a", Config: McpServerConfig{Command: "a"}, ExtensionPath: "two"})
	// Replacing keeps the position, as a Map does.
	registry.Register(RegisteredMcpServer{Name: "b", Config: McpServerConfig{URL: "http://b2.example/mcp"}, ExtensionPath: "one"})
	names := func() string {
		var out []string
		for _, s := range registry.List() {
			out = append(out, s.Name)
		}
		return strings.Join(out, ",")
	}
	if names() != "b,a" {
		t.Fatalf("names = %s", names())
	}
	// Another extension's server is left alone.
	registry.Unregister("a", "one")
	if names() != "b,a" || changes.Load() != 3 {
		t.Fatalf("names = %s, changes = %d", names(), changes.Load())
	}
	registry.Unregister("a", "two")
	if names() != "b" || changes.Load() != 4 {
		t.Fatalf("names = %s, changes = %d", names(), changes.Load())
	}
	// List returns copies.
	listed := registry.List()
	listed[0].Config.URL = "mutated"
	if got, _ := registry.Get("b"); got.Config.URL != "http://b2.example/mcp" {
		t.Fatalf("registry changed through a copy: %s", got.Config.URL)
	}
}

func TestValidateMcpServerConfigMessages(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{"ok", `{"command":"x","args":["a"],"env":{"A":"1"},"cwd":"."}`, ""},
		{"ok", `{"type":"streamable-http","url":"https://x.example/mcp"}`, ""},
		{"bad name", `{"command":"x"}`, `invalid server name "bad name" (use letters, digits, "_" and "-")`},
		{"x", `[]`, `server "x" must be an object`},
		{"x", `{"command":"x","enabled":"no"}`, `server "x": enabled must be a boolean`},
		{"x", `{"command":"x","timeout":0}`, `server "x": timeout must be a positive number of seconds`},
		{"x", `{"command":"x","args":[1]}`, `server "x": args must be an array of strings`},
		{"x", `{"command":"x","env":{"A":1}}`, `server "x": env must map names to strings`},
		{"x", `{"command":"x","cwd":1}`, `server "x": cwd must be a string`},
		{"x", `{"url":"https://x.example","headers":{"A":1}}`, `server "x": headers must map names to strings`},
		{"x", `{"url":"ftp://x.example"}`, `server "x": url must be an http or https URL`},
		{"x", `{"url":"https://x.example","oauth":{"callbackPort":70000}}`, `server "x": oauth.callbackPort must be a port number`},
		{"x", `{"url":"https://x.example","oauth":{"callbackPort":1.5}}`, `server "x": oauth.callbackPort must be a port number`},
		{"x", `{"url":"https://x.example","oauth":"no"}`, `server "x": oauth must be an object`},
		{"x", `{"command":"x","type":"http"}`, `server "x" needs either "command" (stdio) or "url" (streamable HTTP)`},
	} {
		_, message := ValidateMcpServerConfig(tc.name, json.RawMessage(tc.config))
		if message != tc.want {
			t.Errorf("%s %s: message = %q, want %q", tc.name, tc.config, message, tc.want)
		}
	}
}

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
	} {
		if got := IsLoopbackRedirectURI(uri); got != want {
			t.Errorf("IsLoopbackRedirectURI(%q) = %v, want %v", uri, got, want)
		}
	}
}

// mcp-servers.ts validateMcpServerConfig checks auth against `new URL(value.url).hostname`, which the WHATWG URL parser lowercases and, for an IPv6 literal, serializes in its compressed bracketed form. Node prints "localhost" for http://LocalHost:8788 and "[::1]" for http://[0:0:0:0:0:0:0:1].
func TestValidateMcpServerConfigAcceptsProviderAuthOnLoopbackHostsAsTheURLParserNormalizesThem(t *testing.T) {
	// The WHATWG host parser also serializes an IPv4 address in shorthand, hex, or with a trailing dot as a dotted quad:
	// Pi 0.99.2 validateMcpServerConfig accepts auth on http://127.1, http://0x7f000001:8080, and http://127.0.0.1./, and
	// rejects http://localhost./ (a domain keeps its trailing dot).
	for _, rawURL := range []string{"http://LocalHost:8788/mcp", "http://[0:0:0:0:0:0:0:1]:1/mcp", "http://[::1]/mcp", "HTTP://127.0.0.1/mcp", "http://127.1/x", "http://0x7f000001:8080/", "http://127.0.0.1./"} {
		config := json.RawMessage(`{"url":"` + rawURL + `","auth":{"provider":"p"}}`)
		if _, message := ValidateMcpServerConfig("s", config); message != "" {
			t.Errorf("%s: %s", rawURL, message)
		}
	}
	for _, rawURL := range []string{"http://localhost.example/mcp", "http://localhost./"} {
		if _, message := ValidateMcpServerConfig("s", json.RawMessage(`{"url":"`+rawURL+`","auth":{"provider":"p"}}`)); !strings.Contains(message, "auth requires an https URL") {
			t.Errorf("%s: %q", rawURL, message)
		}
	}
}

// mcp-servers.ts validateOAuth rejects `clientName` when `!value.clientName.trim()`. String.prototype.trim removes
// U+FEFF but not U+0085. Probe of Pi 0.99.2 validateMcpServerConfig: "\uFEFF" and " " are rejected, "\u0085" is
// accepted.
func TestValidateMcpServerConfigTrimsTheOAuthClientNameAsJavaScriptDoes(t *testing.T) {
	for clientName, want := range map[string]string{
		"\uFEFF": `server "s": oauth.clientName must be a non-empty string`,
		" ":      `server "s": oauth.clientName must be a non-empty string`,
		"\u0085": "",
		"x":      "",
	} {
		config, _ := json.Marshal(map[string]any{"url": "https://a.example/", "oauth": map[string]any{"clientName": clientName}})
		if _, message := ValidateMcpServerConfig("s", config); message != want {
			t.Errorf("clientName %q: message = %q, want %q", clientName, message, want)
		}
	}
}

// mcp-servers.ts validateMcpServerConfig checks `description` before `timeout`.
func TestValidateMcpServerConfigChecksTheDescriptionBeforeTheTimeout(t *testing.T) {
	_, message := ValidateMcpServerConfig("s", json.RawMessage(`{"command":"x","timeout":0,"description":1}`))
	if want := `server "s": description must be a string`; message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

// mcp-servers.ts validateOAuth rejects `!value.clientName.trim()`: String.prototype.trim strips U+FEFF but not U+0085 or
// U+180E, unlike Go's unicode.IsSpace set.
func TestValidateMcpServerConfigRejectsClientNamesThatStringTrimEmpties(t *testing.T) {
	for _, tc := range []struct {
		clientName string
		rejected   bool
	}{
		{`"\ufeff"`, true},
		{`" \u3000\t"`, true},
		{`"\u0085"`, false},
		{`"\u180e"`, false},
	} {
		raw := json.RawMessage(`{"url":"https://a.example/mcp","oauth":{"clientName":` + tc.clientName + `}}`)
		_, message := ValidateMcpServerConfig("s", raw)
		want := ""
		if tc.rejected {
			want = `server "s": oauth.clientName must be a non-empty string`
		}
		if message != want {
			t.Errorf("clientName %s: message = %q, want %q", tc.clientName, message, want)
		}
	}
}

// mcp-servers.ts validates `url` with `URL.canParse(value.url) && /^https?:$/.test(new URL(value.url).protocol)` and
// compares the auth host as `new URL(value.url).hostname`. The WHATWG parser percent-decodes the host, so Node accepts
// http://%6cocalhost/ as localhost (Node 24 probe, also for isLoopbackRedirectUri); Go's url.Parse rejects it.
func TestValidateMcpServerConfigParsesTheURLAsTheWHATWGParserDoes(t *testing.T) {
	const badURL = `server "s": url must be an http or https URL`
	const badAuth = `server "s": auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]`
	for _, tc := range []struct {
		config string
		want   string
	}{
		{`{"url":"http://%6cocalhost/"}`, ""},
		{`{"url":"http://%6cocalhost:8080/mcp","auth":{"provider":"p"}}`, ""},
		{`{"url":"http:example.com"}`, ""},
		{`{"url":"http:\\\\example.com\\mcp"}`, ""},
		{`{"url":"http://user:pw@127.1/","auth":{"provider":"p"}}`, ""},
		{`{"url":"http://%65xample.com/","auth":{"provider":"p"}}`, badAuth},
		{`{"url":"http://exa%20mple.com/"}`, badURL},
		{`{"url":"http://host:99999/"}`, badURL},
		{`{"url":"http://"}`, badURL},
		{`{"url":"ftp://host/"}`, badURL},
		{`{"url":"not a url"}`, badURL},
	} {
		if _, message := ValidateMcpServerConfig("s", json.RawMessage(tc.config)); message != tc.want {
			t.Errorf("%s: message = %q, want %q", tc.config, message, tc.want)
		}
	}
	for uri, want := range map[string]bool{
		"http://%6cocalhost:8080/cb": true, "http://%6cocalhost/cb?x=1": false, "http:localhost/cb": true,
		"https://%6cocalhost/cb": false, "http://exa%20mple.com/": false,
	} {
		if got := IsLoopbackRedirectURI(uri); got != want {
			t.Errorf("IsLoopbackRedirectURI(%q) = %v, want %v", uri, got, want)
		}
	}
	config := json.RawMessage(`{"url":"https://x.example","oauth":{"callbackUrl":"http://%6cocalhost:9000/cb","callbackPort":9001}}`)
	if _, message := ValidateMcpServerConfig("s", config); message != `server "s": oauth.callbackUrl and oauth.callbackPort name different ports` {
		t.Errorf("callbackUrl port message = %q", message)
	}
}
