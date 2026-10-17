package ai

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's resolveHttpProxyUrlForTarget (utils/node-http-proxy.js) run against the vendored Pi module: which proxy applies to a target URL, the NO_PROXY exclusion rules, and how the proxy URL itself is parsed and serialized (WHATWG).
func TestResolveHTTPProxyURLForTargetMatchesPiOracle(t *testing.T) {
	for _, key := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(key, "")
	}
	targets := []string{
		"https://api.example.com/v1", "http://api.example.com", "HTTPS://API.EXAMPLE.COM:443/x", "https://api.example.com:8443/", "http://[::1]:8080/x", "https://[2001:db8::1]/", "ws://h/x", "wss://h:443/", "ftp://h/", "gopher://h/",
		"http://localhost", "http://127.0.0.1:3000", "https://İ.example/", "https://ΑΣ.example/", "https://ΑΣ/", "https://İ/", "https://sub.internal.example.com", "https://internal.example.com", "https://notinternal.example.com", " https://api.example.com ", "https:\\\\api.example.com\\x",
		"https://u:p@api.example.com/", "https://exämple.com", "http://0x7f.1/", "file:///x", "custom://h/x", "mailto:a@b", "", "not a url", "https://", "https://api.example.com.", "http://a.b:0/", "http://a.b:80/",
	}
	envs := []map[string]string{
		{"https_proxy": "http://proxy:3128", "http_proxy": "http://proxy:3128"},
		{"HTTPS_PROXY": "proxy:3128", "HTTP_PROXY": "proxy:3128", "ws_proxy": "proxy:1", "wss_proxy": "proxy:2", "ftp_proxy": "proxy:3"},
		{"all_proxy": "https://U:P@PROXY.Example.com:8443/path?q=1#f"},
		{"all_proxy": "socks5://proxy:1080"},
		{"all_proxy": "pac+http://x/proxy.pac"},
		{"all_proxy": "HTTP://Proxy"},
		{"all_proxy": "http://proxy:80"},
		{"all_proxy": "https://proxy:443/"},
		{"all_proxy": "http://[::1]:3128"},
		{"all_proxy": "http://proxy with space:8080"},
		{"all_proxy": " http://proxy:8080 "},
		{"all_proxy": "http://pröxy:8080"},
		{"all_proxy": "http://proxy:99999"},
		{"all_proxy": "http://:8080"},
		{"all_proxy": "http://u:p@proxy"},
		{"all_proxy": "http://proxy\\path"},
		{"all_proxy": "http://proxy/a b?c d#e f"},
		{"all_proxy": "http://%70roxy"},
		{"all_proxy": "http://1.2.3"},
		{"all_proxy": "http://0x7f.1:8080"},
		{"all_proxy": "proxy"},
		{"all_proxy": "://x"},
		{"all_proxy": "http://"},
		{"all_proxy": "a:b"},
		{"all_proxy": "localhost:3128"},
		{"all_proxy": "10.0.0.1:3128"},
		{"all_proxy": "[::1]:3128"},
	}
	noProxies := []string{
		"", "*", "api.example.com", ".example.com", "*.example.com", "*example.com", "example.com:443", "example.com:8443", "example.com:0", "example.com:abc", "api.example.com, other", "api.example.com other", "a,b;c.example.com",
		"\ufeffexample.com", "\u0085example.com", "example.com\ufeff", "\u0085", "\ufeff", "İ.example", "ΑΣ.example", "ΑΣ", "İ", "i̇.example", "ας", "localhost:\ufeff80", "localhost:80\u0085", "localhost:\u008580", "localhost:\ufeff80\ufeff", "a\u2028b.example", "example.com\u00a0", "example.com\u1680, localhost",
		"[::1]", "[::1]:8080", "[::1]:80", "::1", "2001:db8::1", "[2001:db8::1]", "localhost,127.0.0.1", "127.0.0.1:3000", "LOCALHOST", "  localhost  ", "localhost:80", "localhost:+80", "localhost:80x", "localhost:-80", ".", "*.", "*", ",", " ,", "internal.example.com",
		".internal.example.com", "h", "h:80", "h:443", "b:80", "a.b:80", "a.b:0", "a.b:00080", "example.com:443abc", "[::1]:", "[::1", "[::1]x", "[]", "[]:80", "api.example.com\t", "api.example.com\n127.0.0.1", "é.example.com", "exämple.com",
		// A port beyond int64 is a huge number for parseInt, never a match, so the entry does not apply; a bracketed entry with one must not cover every port.
		"[::1]:99999999999999999999", "[::1]:-99999999999999999999", "h:99999999999999999999", "[::1]:000000000000000000000080",
	}
	type probe struct {
		Target  string            `json:"target"`
		Env     map[string]string `json:"env"`
		NoProxy string            `json:"noProxy"`
	}
	type result struct {
		Href  string `json:"href"`
		Error string `json:"error"`
		None  bool   `json:"none"`
	}
	var probes []probe
	for _, target := range targets {
		for _, env := range envs {
			probes = append(probes, probe{target, env, ""})
		}
	}
	for _, target := range targets {
		for _, noProxy := range noProxies {
			probes = append(probes, probe{target, map[string]string{"all_proxy": "http://proxy:3128", "no_proxy": noProxy}, noProxy})
		}
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-ai/utils/node-http-proxy.js");
emit(input.map((p) => {
	try {
		const u = mod.resolveHttpProxyUrlForTarget(p.target, p.env);
		return u === undefined ? { none: true } : { href: u.href };
	} catch (e) {
		return { error: e.message };
	}
}));`, probes, &want)
	for i, p := range probes {
		got, err := ResolveHTTPProxyURLForTarget(p.Target, p.Env)
		var have result
		switch {
		case err != nil:
			have.Error = err.Error()
		case got == nil:
			have.None = true
		default:
			have.Href = got.String()
		}
		if have != want[i] {
			t.Errorf("target=%q env=%q:\n Go %+v\n Pi %+v", p.Target, p.Env, have, want[i])
		}
	}
}
