package oauth

import "testing"

// selectClientAuthMethod, row by row (Pi packages/mcp/src/oauth/flow.ts:128-141). The expected values are Pi's ladder: a valid hinted method the
// server supports (or any, when the server lists none) wins; otherwise with no listed methods basic when there is a secret else none; otherwise
// basic, then post (both needing a secret), then none, then post when there is a secret, else none.
// Pi: packages/mcp/src/oauth/flow.ts:128-141
func TestSelectClientAuthMethodFollowsPisLadder(t *testing.T) {
	secret := OAuthClientInformationMixed{ClientSecret: "s"}
	hint := func(method, secretValue string) OAuthClientInformationMixed {
		info := OAuthClientInformationMixed{ClientSecret: secretValue}
		info.TokenEndpointAuthMethod = method
		return info
	}
	for _, tc := range []struct {
		name      string
		info      OAuthClientInformationMixed
		supported []string
		want      string
	}{
		{"hint supported", hint("client_secret_post", "s"), []string{"client_secret_basic", "client_secret_post"}, "client_secret_post"},
		{"hint with no listed methods", hint("none", "s"), nil, "none"},
		{"hint not supported falls through", hint("none", "s"), []string{"client_secret_basic"}, "client_secret_basic"},
		{"unknown hint is ignored", hint("private_key_jwt", "s"), []string{"private_key_jwt"}, "client_secret_post"},
		{"no listed methods with a secret", secret, nil, "client_secret_basic"},
		{"no listed methods without a secret", OAuthClientInformationMixed{}, nil, "none"},
		{"secret and basic", secret, []string{"none", "client_secret_basic", "client_secret_post"}, "client_secret_basic"},
		{"secret and post beats none", secret, []string{"none", "client_secret_post"}, "client_secret_post"},
		{"secret and only none", secret, []string{"none"}, "none"},
		{"no secret skips basic and post", OAuthClientInformationMixed{}, []string{"client_secret_basic", "client_secret_post", "none"}, "none"},
		{"no secret and only secret methods", OAuthClientInformationMixed{}, []string{"client_secret_basic", "client_secret_post"}, "none"},
		{"secret and only unknown methods", secret, []string{"private_key_jwt"}, "client_secret_post"},
		{"no secret and only unknown methods", OAuthClientInformationMixed{}, []string{"private_key_jwt"}, "none"},
	} {
		if got := selectClientAuthMethod(tc.info, tc.supported); got != tc.want {
			t.Errorf("%s: selectClientAuthMethod = %q, want %q", tc.name, got, tc.want)
		}
	}
}
