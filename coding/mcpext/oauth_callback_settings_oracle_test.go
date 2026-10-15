package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// callbackSettings (extensions/mcp/oauth.ts:86-107) is not exported by Pi's module, so the oracle body transcribes it and runs it on Node's own URL implementation: the listen host, redirect host, port, path and exact redirect URI for every callbackUrl spelling and callbackPort.
func TestNewCallbackSettingsMatchesNodeURL(t *testing.T) {
	callbackURLs := []string{
		"", "http://127.0.0.1:8080/callback", "http://127.0.0.1/callback", "http://127.0.0.1:80/callback", "https://example.com:443/cb", "https://example.com/cb?x=1#f", "http://localhost/callback", "http://localhost:3000/callback", "HTTP://LOCALHOST/Callback",
		"http://[::1]/callback", "http://[::1]:8080/callback", "http://[0:0:0:0:0:0:0:1]/callback", "http://127.1/callback", "http://0x7f.0.0.1/callback", "http://exämple.com/cb", "http://127.0.0.1", "http://127.0.0.1/", "http://127.0.0.1/a/../b", "http://127.0.0.1/a b",
		"http://127.0.0.1\\callback", "http://u:p@127.0.0.1/callback", " http://127.0.0.1/callback ", "ws://h/x", "wss://h/x", "ftp://h/x", "custom://h/x", "custom://h", "custom:opaque", "file:///cb", "http://127.0.0.1:99999/", "http://", "relative", "http://127.0.0.1:0080/callback",
	}
	ports := []int{0, 1, 80, 443, 3000, 8080, 65535}
	type probe struct {
		CallbackURL  string `json:"callbackUrl"`
		CallbackPort int    `json:"callbackPort"`
	}
	var probes []probe
	for _, u := range callbackURLs {
		probes = append(probes, probe{u, 0})
		for _, port := range ports[1:] {
			probes = append(probes, probe{u, port})
		}
	}
	type result struct {
		Error        string `json:"error"`
		Host         string `json:"host"`
		RedirectHost string `json:"redirectHost"`
		Port         *int   `json:"port"`
		Path         string `json:"path"`
		Fixed        string `json:"fixed"`
	}
	var want []result
	pioracle.Run(t, `
function callbackSettings(settings) {
	const url = new URL(settings.callbackUrl ?? "http://127.0.0.1/callback");
	const address = url.hostname.replace(/^\[|\]$/g, "");
	const port = url.port ? Number(url.port) : settings.callbackPort;
	let fixedRedirectUrl;
	if (url.port) fixedRedirectUrl = settings.callbackUrl;
	else if (port !== undefined) { url.port = String(port); fixedRedirectUrl = url.href; }
	return { host: address === "localhost" ? "127.0.0.1" : address, redirectHost: address, port, path: url.pathname, fixed: fixedRedirectUrl };
}
emit(input.map((p) => {
	try {
		const r = callbackSettings({ callbackUrl: p.callbackUrl || undefined, callbackPort: p.callbackPort || undefined });
		return { host: r.host, redirectHost: r.redirectHost, port: r.port ?? null, path: r.path, fixed: r.fixed ?? "" };
	} catch (e) { return { error: e.message }; }
}));`, probes, &want)
	for i, p := range probes {
		settings := McpOAuthSettings{CallbackURL: p.CallbackURL}
		if p.CallbackPort != 0 {
			port := p.CallbackPort
			settings.CallbackPort = &port
		}
		var have result
		got, err := newCallbackSettings(settings)
		if err != nil {
			have.Error = "Invalid URL"
		} else {
			have = result{Host: got.host, RedirectHost: got.redirectHost, Port: got.port, Path: got.path, Fixed: got.fixedRedirectURL}
		}
		if have.Error != want[i].Error || have.Host != want[i].Host || have.RedirectHost != want[i].RedirectHost || have.Path != want[i].Path || have.Fixed != want[i].Fixed || (have.Port == nil) != (want[i].Port == nil) || (have.Port != nil && *have.Port != *want[i].Port) {
			t.Errorf("callbackUrl=%q callbackPort=%d:\n Go %+v\n Pi %+v", p.CallbackURL, p.CallbackPort, have, want[i])
		}
	}
}
