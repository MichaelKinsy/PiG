package mcpext

import (
	"net/url"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// responseFromRedirectUrl (extensions/mcp/oauth.ts:395-413) is not exported by Pi's module, so the oracle body transcribes it and runs it on Node's own URL implementation: the pasted URL is parsed, compared with the sign-in's redirect URL by origin and pathname, and read through URLSearchParams.
func TestResponseFromRedirectURLMatchesNodeURL(t *testing.T) {
	redirects := []string{"http://127.0.0.1:8080/callback", "http://127.0.0.1/callback", "https://example.com:443/cb", "http://localhost:3000/", "http://[::1]:9/callback", "https://pi.dev/oauth/x/callback"}
	suffixes := []string{
		"?code=abc&state=S", "?state=S&code=abc&iss=https%3A%2F%2Fissuer", "?code=a%20b+c&state=S&iss=", "?state=S&code=", "?state=S", "?code=abc", "?code=abc&state=T", "?error=access_denied&state=S", "?error=x&error_description=nope+bad&state=S",
		"?error=&code=abc&state=S", "?error_description=d&code=c&state=S", "?code=1&code=2&state=S", "?code=%zz&state=S", "?co%64e=abc&state=S", "?code=é&state=S", "#code=abc&state=S", "?code=abc&state=S#frag", "",
	}
	prefixes := []string{
		"", " ", "\t", "\u00a0", "\ufeff", "\n",
	}
	bases := []string{
		"http://127.0.0.1:8080/callback", "http://127.0.0.1/callback", "http://127.0.0.1:80/callback", "HTTP://127.0.0.1:8080/callback", "http://127.0.0.1:8080/Callback", "http://127.0.0.1:8080/callback/", "http://127.0.0.1:8080/a/../callback",
		"http://127.0.0.1:8080/%63allback", "http://127.0.0.1:8080\\callback", "http://127.0.0.1:8080//callback", "http://localhost:8080/callback", "http://127.1:8080/callback", "http://0x7f.0.0.1:8080/callback", "http://127.0.0.1:8081/callback",
		"https://example.com:443/cb", "https://example.com/cb", "https://EXAMPLE.com/cb", "https://exämple.com/cb", "https://example.com:8443/cb", "http://example.com/cb", "http://localhost:3000/", "http://localhost:3000", "http://localhost:3000/x",
		"http://[::1]:9/callback", "http://[0:0:0:0:0:0:0:1]:9/callback", "https://pi.dev/oauth/x/callback", "https://u:p@pi.dev/oauth/x/callback", "custom://h/callback", "file:///callback", "ftp://h/callback", "mailto:a@b", "javascript:1",
		"127.0.0.1:8080/callback", "/callback", "callback", "not a url", "",
	}
	type probe struct {
		Input    string `json:"input"`
		Redirect string `json:"redirect"`
	}
	var probes []probe
	for _, redirect := range redirects {
		for _, base := range bases {
			for _, suffix := range suffixes {
				probes = append(probes, probe{base + suffix, redirect})
			}
		}
		for _, prefix := range prefixes {
			probes = append(probes, probe{prefix + redirect + "?code=abc&state=S" + prefix, redirect})
		}
	}
	type result struct {
		Error string  `json:"error"`
		Code  string  `json:"code"`
		Iss   *string `json:"iss"`
	}
	var want []result
	pioracle.Run(t, `
function responseFromRedirectUrl(input, state, redirectUrl) {
	let url;
	try { url = new URL(input.trim()); } catch { throw new Error("Expected the full redirect URL from the browser address bar"); }
	if (url.origin !== redirectUrl.origin || url.pathname !== redirectUrl.pathname) throw new Error("The redirect URL does not match this sign-in's redirect URI");
	const error = url.searchParams.get("error");
	if (error) throw new Error(url.searchParams.get("error_description") ?? error);
	if (url.searchParams.get("state") !== state) throw new Error("The redirect URL belongs to a different sign-in");
	const code = url.searchParams.get("code");
	if (!code) throw new Error("The redirect URL does not contain an authorization code");
	return { code, iss: url.searchParams.get("iss") ?? undefined };
}
emit(input.map(({ input, redirect }) => {
	try { const r = responseFromRedirectUrl(input, "S", new URL(redirect)); return { code: r.code, iss: r.iss ?? null }; } catch (e) { return { error: e.message }; }
}));`, probes, &want)
	for i, p := range probes {
		redirect, err := url.Parse(p.Redirect)
		if err != nil {
			t.Fatal(err)
		}
		var have result
		response, err := responseFromRedirectURL(p.Input, "S", redirect)
		if err != nil {
			have.Error = err.Error()
		} else {
			have.Code, have.Iss = response.code, response.iss
		}
		if have.Error != want[i].Error || have.Code != want[i].Code || (have.Iss == nil) != (want[i].Iss == nil) || (have.Iss != nil && *have.Iss != *want[i].Iss) {
			t.Errorf("input=%q redirect=%q:\n Go %+v\n Pi %+v", p.Input, p.Redirect, have, want[i])
		}
	}
}
