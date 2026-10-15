package oauth

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's safeUrl (oauth/types.js) through parseProtectedResourceMetadata and parseAuthorizationServerMetadata, run against the vendored Pi module: which URL spellings Node's URL.canParse accepts and which schemes are refused.
func TestMetadataURLValidationMatchesPiOracle(t *testing.T) {
	spellings := []string{
		"https://auth.example.com", "https://auth.example.com/path?x=1#f", "HTTPS://AUTH.EXAMPLE.COM", " https://auth.example.com ", "https://auth.example.com\t", "https://auth.example.com\\x", "https:\\\\auth.example.com", "https://u:p@auth.example.com",
		"https://auth.example.com:99999", "https://auth.example.com:0", "https://exämple.com", "https://a b.example.com", "https://%41.example.com", "https://1.2.3", "https://0x7f.1", "https://[::1]", "https://[::1", "https://[1:2]", "https://",
		"https:", "https:/auth.example.com", "https:auth.example.com", "http://localhost", "ftp://h/x", "ws://h", "file:///x", "file://h/x", "custom://h", "custom:opaque", "custom:", "mailto:a@b", "urn:ietf:params:oauth", "a:b",
		"javascript:alert(1)", "JAVASCRIPT:alert(1)", " javascript:alert(1)", "\tjavascript:alert(1)", "java\tscript:alert(1)", "java\nscript:alert(1)", "data:text/plain,x", "DATA:text/plain,x", "vbscript:x", "VBScript:x", "blob:https://x/y",
		"about:blank", "//auth.example.com", "/path", "relative", "", " ", "not a url", "http//x", "://x", "1http://x", "ht tp://x", "h+t-t.p://x", "x:\u2028", "\u00a0https://x",
	}
	type probe struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	var probes []probe
	for _, s := range spellings {
		for _, kind := range []string{"resource", "server", "issuer", "authorization_endpoint", "token_endpoint", "registration_endpoint"} {
			probes = append(probes, probe{kind, s})
		}
	}
	type result struct {
		Error string `json:"error"`
		Value string `json:"value"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-mcp/oauth/types.js");
const doc = (kind, v) => {
	const base = { issuer: "https://i.example", authorization_endpoint: "https://i.example/a", token_endpoint: "https://i.example/t", response_types_supported: ["code"] };
	if (kind === "resource") return ["p", { resource: v }];
	if (kind === "server") return ["p", { resource: "https://r.example", authorization_servers: [v] }];
	return ["a", { ...base, [kind]: v }];
};
emit(input.map(({ kind, value }) => {
	const [which, body] = doc(kind, value);
	try {
		const r = which === "p" ? mod.parseProtectedResourceMetadata(body) : mod.parseAuthorizationServerMetadata(body);
		return { value: kind === "resource" ? r.resource : kind === "server" ? r.authorization_servers[0] : r[kind] };
	} catch (e) {
		return { error: e.message };
	}
}));`, probes, &want)
	for i, p := range probes {
		var base map[string]any
		switch p.Kind {
		case "resource":
			base = map[string]any{"resource": p.Value}
		case "server":
			base = map[string]any{"resource": "https://r.example", "authorization_servers": []string{p.Value}}
		default:
			base = map[string]any{"issuer": "https://i.example", "authorization_endpoint": "https://i.example/a", "token_endpoint": "https://i.example/t", "response_types_supported": []string{"code"}}
			base[p.Kind] = p.Value
		}
		data, _ := json.Marshal(base)
		var have result
		switch p.Kind {
		case "resource", "server":
			m, err := ParseProtectedResourceMetadata(data)
			switch {
			case err != nil:
				have.Error = err.Error()
			case p.Kind == "resource":
				have.Value = m.Resource
			default:
				have.Value = m.AuthorizationServers[0]
			}
		default:
			m, err := ParseAuthorizationServerMetadata(data)
			if err != nil {
				have.Error = err.Error()
			} else {
				switch p.Kind {
				case "issuer":
					have.Value = m.Issuer
				case "authorization_endpoint":
					have.Value = m.AuthorizationEndpoint
				case "token_endpoint":
					have.Value = m.TokenEndpoint
				default:
					have.Value = m.RegistrationEndpoint
				}
			}
		}
		if have != want[i] {
			t.Errorf("%s=%q:\n Go %+v\n Pi %+v", p.Kind, p.Value, have, want[i])
		}
	}
}
