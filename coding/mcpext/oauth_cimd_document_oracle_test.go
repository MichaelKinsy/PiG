package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// clientMetadataDocument and callbackId (extensions/mcp/oauth.ts:238-271) are not exported by Pi's module, so the oracle body transcribes them and runs them on Node's own URL: the callback ID hashes String(new URL(serverUrl)) without its fragment, and the server-specific redirect URI is new URL(redirectUrl) with pathname "/callback/<id>".
func TestClientMetadataDocumentMatchesNodeURL(t *testing.T) {
	serverURLs := []string{
		"https://mcp.example.com/mcp", "https://mcp.example.com", "https://mcp.example.com/mcp#frag", "https://mcp.example.com/mcp?x=1#frag", "HTTPS://MCP.EXAMPLE.COM:443/Mcp", "https://mcp.example.com:8443/mcp", "https://mcp.example.com/a/../b",
		"https://mcp.example.com/a b?q=é", "https://exämple.com/mcp", " https://mcp.example.com/mcp ", "https://mcp.example.com\\mcp", "http://[0:0:0:0:0:0:0:1]:3000/mcp", "http://127.1/mcp", "https://u:p@mcp.example.com/mcp", "custom://h/x#f", "https://", "", "relative",
	}
	redirectURLs := []string{"http://127.0.0.1:8080/callback", "http://127.0.0.1/callback", "http://localhost:3000/callback?x=1#f", "https://example.com:443/callback", "http://[::1]:9/callback", "HTTP://LOCALHOST:80/Callback", "http://127.0.0.1:8080", "custom://h/callback", "not a url"}
	type probe struct {
		Server   string `json:"server"`
		Redirect string `json:"redirect"`
	}
	var probes []probe
	for _, server := range serverURLs {
		for _, redirect := range redirectURLs {
			probes = append(probes, probe{server, redirect})
		}
	}
	type result struct {
		Error    string `json:"error"`
		URL      string `json:"url"`
		Redirect string `json:"redirect"`
	}
	var want []result
	pioracle.Run(t, `
const { createHash } = await import("node:crypto");
function callbackId(serverUrl) {
	const url = new URL(serverUrl);
	url.hash = "";
	return createHash("sha256").update(url.href).digest().subarray(0, 9).toString("base64url");
}
emit(input.map(({ server, redirect }) => {
	try {
		const id = callbackId(server);
		const r = new URL(redirect);
		r.pathname = "/callback/" + id;
		return { url: "https://pi.dev/oauth/" + id + "/client.json", redirect: r.href };
	} catch (e) { return { error: e.message }; }
}));`, probes, &want)
	supported := true
	metadata := &oauth.AuthorizationServerMetadata{ClientIDMetadataDocumentSupported: &supported, TokenEndpointAuthMethodsSupported: []string{"none"}}
	for i, p := range probes {
		var have result
		doc, err := clientMetadataDocument(p.Server, p.Redirect, metadata)
		if err != nil {
			have.Error = "Invalid URL"
		} else {
			have = result{URL: doc.URL, Redirect: doc.RedirectURL}
		}
		if have != want[i] {
			t.Errorf("server=%q redirect=%q:\n Go %+v\n Pi %+v", p.Server, p.Redirect, have, want[i])
		}
	}
}
