package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's McpOAuthCredentialStore keys a server's state in mcp-auth.json by `${namespace}|${String(new URL(serverUrl))}`. The file is shared with Pi, so the key must be the WHATWG href for every URL spelling.
func TestMcpOAuthStoreKeysMatchPiOracle(t *testing.T) {
	urls := []string{
		"https://mcp.example.com/mcp", "https://mcp.example.com", "https://mcp.example.com/", "HTTPS://MCP.EXAMPLE.COM/Path", "https://mcp.example.com:443/mcp", "http://mcp.example.com:80/mcp", "https://mcp.example.com:8443/mcp",
		"https://mcp.example.com/a/../b/./c", "https://mcp.example.com/a b", "https://mcp.example.com/é", "https://mcp.example.com/%7efoo", "https://mcp.example.com/?q=a b&x=é#frag", "https://mcp.example.com?x=1", "https://mcp.example.com#f",
		"https://exämple.com/mcp", "https://u:p@mcp.example.com/mcp", "https://u@mcp.example.com", "http://[::1]:3000/mcp", "http://[0:0:0:0:0:0:0:1]/mcp", "http://127.1/mcp", "http://0x7f.1:8080/", "https://mcp.example.com./mcp",
		" https://mcp.example.com/mcp ", "https://mcp.example.com/m\tcp", "https://mcp.example.com\\mcp", "https:\\\\mcp.example.com\\mcp", "https:/mcp.example.com/mcp", "https:mcp.example.com/mcp", "https://mcp.example.com/mcp?", "https://mcp.example.com/mcp#",
		"ws://h/x", "wss://h:443/x", "ftp://h:21/x", "file:///tmp/x", "custom://Host/Path", "custom:opaque", "mailto:a@b", "https://", "", "not a url", "/relative", "https://mcp.example.com:99999/", "https://a b/",
	}
	names := []string{"server", "My Server", "a.b", "é"}
	type probe struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	var probes []probe
	for _, name := range names {
		for _, u := range urls {
			probes = append(probes, probe{name, u})
		}
	}
	type result struct {
		Key   string `json:"key"`
		Error string `json:"error"`
	}
	var want []result
	pioracle.Run(t, `
const { McpOAuthCredentialStore } = await load("pi-coding-agent/extensions/mcp/oauth.js");
emit(await Promise.all(input.map(async ({ name, url }) => {
	let text = "";
	const backend = { withLock: async (fn) => { const r = fn(text || undefined); if (r.next !== undefined) text = r.next; return r.result; } };
	try {
		await new McpOAuthCredentialStore(backend).forServer(name, url).save({ marker: 1 });
		return { key: Object.keys(JSON.parse(text))[0] };
	} catch (e) {
		return { error: e.message };
	}
})));`, probes, &want)
	for i, p := range probes {
		key, _, err := storeKeys(p.Name, p.URL)
		have := result{Key: key}
		if err != nil {
			have = result{Error: "Invalid URL"}
		}
		if have != want[i] {
			t.Errorf("name=%q url=%q:\n Go %+v\n Pi %+v", p.Name, p.URL, have, want[i])
		}
	}
}
