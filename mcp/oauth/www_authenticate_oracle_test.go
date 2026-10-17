package oauth

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's parseWwwAuthenticate (oauth/discovery.js), run against the vendored Pi module: scheme detection, the case-folded `name=value` fields with quoted or bare values, and the resource_metadata URL.
func TestParseWWWAuthenticateMatchesPiOracle(t *testing.T) {
	headers := []string{
		"", "Bearer", "bearer realm=\"x\"", "BEARER resource_metadata=\"https://r.example/.well-known/oauth-protected-resource\", scope=\"a b\"", "DPoP error=\"invalid_token\", error_description=\"expired\"", "Basic realm=x", "Negotiate", "Bearerx scope=a",
		"  Bearer scope=a", "\tBearer\tscope=a", "\u00a0Bearer scope=a", "\ufeffBearer scope=a", "Bearer\u00a0scope=a", "Bearer scope=a,error=b", "Bearer scope=a error=b", "Bearer scope=\"\"", "Bearer scope=\"\", scope=b", "Bearer scope=, error=b", "Bearer scope=\"a\"b", "Bearer scope=\"unterminated",
		"Bearer SCOPE=a", "Bearer Resource_Metadata=\"https://r.example/x\"", "Bearer \u212aey=a", "Bearer scope=a scope=b", "Bearer xscope=a", "Bearer x,scope=a", "Bearer x\u00a0scope=a", "Bearer x\u2028scope=a", "Bearer scope=a\u00a0b", "Bearer scope=a\u2003b",
		"Bearer scope=é, error=ü", "Bearer scope=\"é \u00a0 x\"", "Bearer scope=\"a\nb\"", "Bearer\nscope=a", "Bearer scope = a", "Bearer scope =a", "Bearer scope= a", "Bearer scope==a", "Bearer scope=a=b", "Bearer error_description=\"a, b\", error=c",
		"Bearer resource_metadata=\"not a url\"", "Bearer resource_metadata=\"\"", "Bearer resource_metadata=\" https://r.example/x \"", "Bearer resource_metadata=https://r.example/x?a=1,b=2", "Bearer resource_metadata=\"javascript:alert(1)\"", "Bearer resource_metadata=\"HTTPS://R.EXAMPLE:443/x\"",
		"Bearer resource_metadata=\"//r.example/x\"", "Bearer resource_metadata=\"/x\"", "Bearer resource_metadata=\"custom:opaque\"", "Bearer resource_metadata=\"https://\"", "Bearer realm=\"scope=hidden\"", "Bearer realm=\"x\",scope=y", "Bearer realm=\"x\" , scope=y",
		"Bearer error=invalid_request error_description=\"two words\"", "Bearer ,scope=a", ",Bearer scope=a", "Bearer\r\nscope=a", "Bearer scope=\"a\",scope=\"b\"",
	}
	type challenge struct {
		URL              string `json:"url"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"errorDescription"`
	}
	var want []challenge
	pioracle.Run(t, `
const mod = await load("pi-mcp/oauth/discovery.js");
emit(input.map((h) => {
	const c = mod.parseWwwAuthenticate(h);
	return { url: c.resourceMetadataUrl?.href ?? "", scope: c.scope ?? "", error: c.error ?? "", errorDescription: c.errorDescription ?? "" };
}));`, headers, &want)
	for i, header := range headers {
		c := ParseWWWAuthenticate(header)
		have := challenge{Scope: c.Scope, Error: c.Error, ErrorDescription: c.ErrorDescription}
		if c.ResourceMetadataURL != nil {
			have.URL = c.ResourceMetadataURL.String()
		}
		if have != want[i] {
			t.Errorf("header=%q:\n Go %+v\n Pi %+v", header, have, want[i])
		}
	}
}
