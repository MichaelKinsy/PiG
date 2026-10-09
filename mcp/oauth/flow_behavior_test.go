package oauth_test

// Pins packages/mcp/src/oauth/flow.ts behavior that packages/mcp/test/oauth.test.ts leaves open: the PKCE challenge
// startAuthorization derives (flow.ts:140-175), its refusal of servers without code or S256 support, and the client
// authentication method a token request uses (selectClientAuthMethod flow.ts:125-138, applyClientAuthentication
// flow.ts:140-155).

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

func TestStartAuthorizationSendsTheS256ChallengeOfTheReturnedVerifier(t *testing.T) {
	metadata := &oauth.AuthorizationServerMetadata{
		AuthorizationEndpoint:         "https://auth.example.com/authorize",
		ResponseTypesSupported:        []string{"code"},
		CodeChallengeMethodsSupported: []string{"S256"},
	}
	authorizationURL, verifier, err := oauth.StartAuthorization("https://auth.example.com", oauth.StartAuthorizationOptions{
		Metadata: metadata, ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "client"}, RedirectURL: "http://localhost/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	query := authorizationURL.Query()
	digest := sha256.Sum256([]byte(verifier))
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatalf("challenge = %q (%q), want the unpadded base64url SHA-256 of the verifier %q", query.Get("code_challenge"), query.Get("code_challenge_method"), verifier)
	}
	if len(verifier) != 43 || strings.ContainsAny(verifier, "+/=") {
		t.Fatalf("verifier = %q, want 43 base64url characters (32 random bytes)", verifier)
	}

	for _, tc := range []struct {
		name     string
		metadata oauth.AuthorizationServerMetadata
		want     string
	}{
		{"no code response type", oauth.AuthorizationServerMetadata{AuthorizationEndpoint: "https://auth.example.com/authorize", ResponseTypesSupported: []string{"token"}}, "Authorization server does not support authorization codes"},
		{"no S256", oauth.AuthorizationServerMetadata{AuthorizationEndpoint: "https://auth.example.com/authorize", ResponseTypesSupported: []string{"code"}, CodeChallengeMethodsSupported: []string{"plain"}}, "Authorization server does not support PKCE S256"},
	} {
		_, _, err := oauth.StartAuthorization("https://auth.example.com", oauth.StartAuthorizationOptions{
			Metadata: &tc.metadata, ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "client"}, RedirectURL: "http://localhost/callback",
		})
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestTokenRequestsAuthenticateTheClientWithTheMethodTheServerPolicySelects drives selectClientAuthMethod and
// applyClientAuthentication through a token request: a hint the server supports wins, then basic, then post, then none.
func TestTokenRequestsAuthenticateTheClientWithTheMethodTheServerPolicySelects(t *testing.T) {
	for _, tc := range []struct {
		name          string
		information   oauth.OAuthClientInformationMixed
		supported     []string
		wantAuth      string
		wantClientID  bool
		wantSecretKey bool
	}{
		{"secret without a policy uses basic", oauth.OAuthClientInformationMixed{ClientID: "id", ClientSecret: "s"}, nil, "Basic " + base64.StdEncoding.EncodeToString([]byte("id:s")), false, false},
		{"no secret without a policy sends only the id", oauth.OAuthClientInformationMixed{ClientID: "id"}, nil, "", true, false},
		{"basic is preferred to post", oauth.OAuthClientInformationMixed{ClientID: "id", ClientSecret: "s"}, []string{"client_secret_post", "client_secret_basic"}, "Basic " + base64.StdEncoding.EncodeToString([]byte("id:s")), false, false},
		{"post when basic is unsupported", oauth.OAuthClientInformationMixed{ClientID: "id", ClientSecret: "s"}, []string{"client_secret_post"}, "", true, true},
		{"none sends no secret", oauth.OAuthClientInformationMixed{ClientID: "id", ClientSecret: "s"}, []string{"none"}, "", true, false},
		{"a supported hint wins", hinted(oauth.OAuthClientInformationMixed{ClientID: "id", ClientSecret: "s"}, "client_secret_post"), []string{"client_secret_basic", "client_secret_post"}, "", true, true},
		{"an unsupported hint is ignored", hinted(oauth.OAuthClientInformationMixed{ClientID: "id", ClientSecret: "s"}, "client_secret_post"), []string{"client_secret_basic"}, "Basic " + base64.StdEncoding.EncodeToString([]byte("id:s")), false, false},
		{"an unknown hint is ignored", hinted(oauth.OAuthClientInformationMixed{ClientID: "id"}, "private_key_jwt"), nil, "", true, false},
		{"post when nothing listed matches and a secret exists", oauth.OAuthClientInformationMixed{ClientID: "id", ClientSecret: "s"}, []string{"private_key_jwt"}, "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var authorization string
			var form url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				authorization = r.Header.Get("Authorization")
				form, _ = url.ParseQuery(string(body))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"a","token_type":"Bearer"}`)
			}))
			defer server.Close()
			_, err := oauth.ExchangeAuthorizationCode(t.Context(), server.URL, oauth.ExchangeAuthorizationCodeOptions{
				TokenRequestOptions: oauth.TokenRequestOptions{
					Metadata:          &oauth.AuthorizationServerMetadata{TokenEndpoint: server.URL + "/token", TokenEndpointAuthMethodsSupported: tc.supported},
					ClientInformation: tc.information,
				},
				Code: "c", CodeVerifier: "v", RedirectURL: "http://localhost/callback",
			})
			if err != nil {
				t.Fatal(err)
			}
			if authorization != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", authorization, tc.wantAuth)
			}
			if (form.Get("client_id") != "") != tc.wantClientID || (form.Get("client_secret") != "") != tc.wantSecretKey {
				t.Errorf("form client_id = %q, client_secret = %q; want id %v, secret %v", form.Get("client_id"), form.Get("client_secret"), tc.wantClientID, tc.wantSecretKey)
			}
		})
	}
}

// hinted sets the token_endpoint_auth_method a registration response reported.
func hinted(information oauth.OAuthClientInformationMixed, method string) oauth.OAuthClientInformationMixed {
	information.TokenEndpointAuthMethod = method
	return information
}
