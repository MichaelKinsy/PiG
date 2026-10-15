package oauth_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// StartAuthorization builds the URL with `new URL(endpoint)` and `searchParams.set(...)` (flow.ts startAuthorization), so a query already on the
// endpoint is re-serialised by the WHATWG application/x-www-form-urlencoded parser and serializer: every well-formed %XX is decoded, a stray `%`
// or invalid %XX stays literal (and is re-encoded as %25), invalid UTF-8 becomes U+FFFD, `+` is a space. The expected queries below were produced by
// Node's URL on those inputs (the parameters StartAuthorization appends are cut from the end).
// Pi: packages/mcp/src/oauth/flow.ts (startAuthorization), Node `new URL(...).searchParams.set`
func TestStartAuthorizationReserialisesTheEndpointQueryLikeURLSearchParams(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"a=%zz%41", "a=%25zzA"},
		{"a=%41%", "a=A%25"},
		{"%zz=1&b=%e0%a4%a", "%25zz=1&b=%EF%BF%BD%25a"},
		{"x=a+b%2Bc", "x=a+b%2Bc"},
		{"=v&k", "=v&k="},
		{"a=%C3%28", "a=%EF%BF%BD%28"},
	} {
		metadata := &oauth.AuthorizationServerMetadata{
			AuthorizationEndpoint:         "https://auth.example.com/authorize?" + tc.query,
			ResponseTypesSupported:        []string{"code"},
			CodeChallengeMethodsSupported: []string{"S256"},
		}
		authorizationURL, _, err := oauth.StartAuthorization("https://auth.example.com", oauth.StartAuthorizationOptions{
			Metadata: metadata, ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "client"}, RedirectURL: "http://localhost/callback",
		})
		if err != nil {
			t.Fatalf("%q: %v", tc.query, err)
		}
		got, _, _ := strings.Cut(authorizationURL.Query, "&response_type=")
		if got != tc.want {
			t.Errorf("endpoint query %q: re-serialised as %q, want %q (Node URLSearchParams)", tc.query, got, tc.want)
		}
	}
}
