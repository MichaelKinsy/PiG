package oauth_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// types.ts absent(): `null` and `""` in an optional token response field are absent, and `expires_in: null` is no
// expiry rather than `Number(null)`, which is 0 and would expire the token at once.
func TestParseOAuthTokensTreatsEmptyAndNullOptionalFieldsAsAbsent(t *testing.T) {
	tokens, err := oauth.ParseOAuthTokens([]byte(`{"access_token":"a","token_type":"Bearer","scope":"","refresh_token":null,"id_token":"","expires_in":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := (oauth.OAuthTokens{AccessToken: "a", TokenType: "Bearer"}); !reflect.DeepEqual(*tokens, want) {
		t.Fatalf("tokens = %#v", *tokens)
	}
	tokens, err = oauth.ParseOAuthTokens([]byte(`{"access_token":"a","token_type":"Bearer","expires_in":""}`))
	if err != nil || tokens.ExpiresIn != nil {
		t.Fatalf("empty expires_in: %#v, %v", tokens, err)
	}
	// A present value of the wrong type stays invalid.
	if _, err := oauth.ParseOAuthTokens([]byte(`{"access_token":"a","token_type":"Bearer","scope":7}`)); err == nil || err.Error() != "Invalid scope" {
		t.Fatalf("numeric scope: err = %v", err)
	}
	// The required fields are not optional.
	if _, err := oauth.ParseOAuthTokens([]byte(`{"access_token":"a","token_type":null}`)); err == nil || err.Error() != "Invalid token_type" {
		t.Fatalf("null token_type: err = %v", err)
	}
}

func TestParseClientInformationTreatsEmptyAndNullOptionalFieldsAsAbsent(t *testing.T) {
	for _, secret := range []string{`""`, `null`} {
		info, err := oauth.ParseClientInformation([]byte(`{"client_id":"c","client_secret":` + secret + `,"redirect_uris":null}`))
		if err != nil {
			t.Fatalf("client_secret %s: %v", secret, err)
		}
		if info.ClientSecret != "" || info.RedirectURIs == nil || len(info.RedirectURIs) != 0 {
			t.Fatalf("client_secret %s: %#v", secret, info)
		}
	}
}

func TestParseAuthorizationServerMetadataReadsTheIssParameterSupportAndAbsentFields(t *testing.T) {
	base := `"issuer":"https://as.example","authorization_endpoint":"https://as.example/a","token_endpoint":"https://as.example/t","response_types_supported":["code"]`
	metadata, err := oauth.ParseAuthorizationServerMetadata([]byte(`{` + base + `,"registration_endpoint":"","scopes_supported":null,"authorization_response_iss_parameter_supported":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.RegistrationEndpoint != "" || metadata.ScopesSupported != nil {
		t.Fatalf("absent fields = %q, %#v", metadata.RegistrationEndpoint, metadata.ScopesSupported)
	}
	if metadata.AuthorizationResponseIssParameterSupported == nil || !*metadata.AuthorizationResponseIssParameterSupported {
		t.Fatalf("iss parameter support = %v", metadata.AuthorizationResponseIssParameterSupported)
	}
	if _, extra := metadata.Extra["authorization_response_iss_parameter_supported"]; extra {
		t.Fatal("the modeled member is also kept as an extra")
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(data, &roundTrip); err != nil || roundTrip["authorization_response_iss_parameter_supported"] != true {
		t.Fatalf("marshaled = %s", data)
	}
	// A non-boolean value is dropped, as `typeof … === "boolean"` drops it.
	metadata, err = oauth.ParseAuthorizationServerMetadata([]byte(`{` + base + `,"authorization_response_iss_parameter_supported":"yes"}`))
	if err != nil || metadata.AuthorizationResponseIssParameterSupported != nil {
		t.Fatalf("string support: %#v, %v", metadata, err)
	}
	if _, err := oauth.ParseAuthorizationServerMetadata([]byte(`{` + base[:len(base)-len(`,"response_types_supported":["code"]`)] + `,"response_types_supported":null}`)); err == nil || err.Error() != "Invalid response_types_supported" {
		t.Fatalf("null response types: err = %v", err)
	}
}

// types.ts safeUrl: an unparsable URL fails as `Invalid <name>`, not as the `TypeError` that discovery propagates.
func TestParseProtectedResourceMetadataRejectsAnUnparsableAuthorizationServerAsInvalid(t *testing.T) {
	_, err := oauth.ParseProtectedResourceMetadata([]byte(`{"resource":"https://mcp.example/mcp","authorization_servers":["not a url"]}`))
	if err == nil || err.Error() != "Invalid authorization server URL" {
		t.Fatalf("err = %v", err)
	}
	metadata, err := oauth.ParseProtectedResourceMetadata([]byte(`{"resource":"https://mcp.example/mcp","authorization_servers":null,"scopes_supported":null}`))
	if err != nil || metadata.AuthorizationServers != nil || metadata.ScopesSupported != nil {
		t.Fatalf("null lists: %#v, %v", metadata, err)
	}
}

func TestOAuthIssuerMismatchErrorNamesAMissingIssAsNone(t *testing.T) {
	received := `https://attacker.example/"x"`
	cases := map[string]*oauth.OAuthIssuerMismatchError{
		`OAuth issuer mismatch: expected "https://as.example", received none`:                             {Expected: "https://as.example"},
		`OAuth issuer mismatch: expected "https://as.example", received "https://attacker.example/\"x\""`: {Expected: "https://as.example", Received: &received},
	}
	for want, err := range cases {
		if err.Error() != want {
			t.Fatalf("message = %s, want %s", err.Error(), want)
		}
	}
}

// errors.ts OAuthIssuerMismatchError: the issuers are quoted with JSON.stringify, which keeps U+2028, U+2029, DEL
// and `<>&` as they are and escapes C0 controls with lowercase hex or their short forms. Node prints
// JSON.stringify("a\u2028b\u2029c\u0001\u007f<>&\b\t\n\f\r\"\\") with exactly these escapes.
func TestOAuthIssuerMismatchErrorQuotesIssuersAsJSONStringify(t *testing.T) {
	received := "a\u2028b\u2029c\u0001\u007f<>&\b\t\n\f\r\"\\"
	err := &oauth.OAuthIssuerMismatchError{Expected: "https://as.example/\u2028", Received: &received}
	want := "OAuth issuer mismatch: expected \"https://as.example/\u2028\", received \"a\u2028b\u2029c\\u0001\u007f<>&\\b\\t\\n\\f\\r\\\"\\\\\""
	if got := err.Error(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestStepUpScopeAddsTheChallengedScopesToTheGrantedOnes(t *testing.T) {
	cases := []struct{ granted, challenged, want string }{
		{"repo read:org", "repo admin", "repo read:org admin"},
		{"", "admin", "admin"},
		{"repo", "", ""},
		{"  a\tb  ", "b\u00a0c", "a b c"},
		// JavaScript `\s` includes U+FEFF but not U+0085.
		{"a\ufeffb", "c\u0085d", "a b c\u0085d"},
	}
	for _, c := range cases {
		if got := oauth.StepUpScope(c.granted, c.challenged); got != c.want {
			t.Fatalf("StepUpScope(%q, %q) = %q, want %q", c.granted, c.challenged, got, c.want)
		}
	}
}

// flow.ts runFlow: a present but empty `iss` is checked even when the server does not promise the parameter, and an
// empty `scopes_supported` falls through to the client's scope.
func TestAuthorizeMcpChecksAPresentEmptyIssAndSkipsAnEmptyAdvertisedScope(t *testing.T) {
	ctx := t.Context()
	exchanged := 0
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			jsonResponse(w, 200, map[string]any{"resource": serverOrigin + "/mcp", "scopes_supported": []string{}})
		case "/.well-known/oauth-authorization-server":
			jsonResponse(w, 200, map[string]any{
				"issuer": serverOrigin, "authorization_endpoint": serverOrigin + "/authorize", "token_endpoint": serverOrigin + "/token",
				"response_types_supported": []string{"code"},
			})
		case "/token":
			exchanged++
			jsonResponse(w, 200, map[string]any{"access_token": "token", "token_type": "Bearer"})
		default:
			w.WriteHeader(404)
		}
	})
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	provider.client = &oauth.OAuthClientInformationMixed{ClientID: "client", OAuthClientMetadata: oauth.OAuthClientMetadata{Scope: "client-scope"}}
	scoped := &scopedProvider{testOAuthProvider: provider, scope: "client-scope"}
	result, err := oauth.AuthorizeMcp(ctx, scoped, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp"})
	if err != nil || result != oauth.OAuthRedirect {
		t.Fatalf("authorize = %q, %v", result, err)
	}
	if got := provider.authorizationURL.Query().Get("scope"); got != "client-scope" {
		t.Fatalf("scope = %q", got)
	}
	provider.verifier = "verifier"
	empty := ""
	_, err = oauth.AuthorizeMcp(ctx, scoped, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp", AuthorizationCode: "code", Iss: &empty})
	var mismatch *oauth.OAuthIssuerMismatchError
	if !errors.As(err, &mismatch) || mismatch.Received == nil || *mismatch.Received != "" {
		t.Fatalf("empty iss: err = %v", err)
	}
	if exchanged != 0 {
		t.Fatalf("a rejected code was exchanged %d times", exchanged)
	}
	// The exchanged token records the requested scope, since the response names none.
	if _, err := oauth.AuthorizeMcp(ctx, scoped, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp", AuthorizationCode: "code"}); err != nil {
		t.Fatal(err)
	}
	if provider.tokenSet == nil || provider.tokenSet.Scope != "client-scope" {
		t.Fatalf("tokens = %#v", provider.tokenSet)
	}
}

// flow.ts runFlow: with a configured metadata URL, cached discovery is neither used nor replaced.
func TestAuthorizeMcpWithAConfiguredMetadataURLBypassesCachedDiscovery(t *testing.T) {
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		if r.URL.Path == "/idp.json" {
			jsonResponse(w, 200, map[string]any{
				"issuer": "https://idp.example", "authorization_endpoint": serverOrigin + "/idp/authorize", "token_endpoint": serverOrigin + "/idp/token",
				"response_types_supported": []string{"code"},
			})
			return
		}
		w.WriteHeader(404)
	})
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	provider.client = &oauth.OAuthClientInformationMixed{ClientID: "client"}
	cached := &oauth.OAuthDiscoveryState{
		AuthorizationServerURL: "https://cached.example",
		AuthorizationServerMetadata: &oauth.AuthorizationServerMetadata{
			Issuer: "https://cached.example", AuthorizationEndpoint: "https://cached.example/authorize", TokenEndpoint: "https://cached.example/token",
			ResponseTypesSupported: []string{"code"},
		},
	}
	provider.discovery = cached
	metadataURL, _ := url.Parse(origin + "/idp.json")
	result, err := oauth.AuthorizeMcp(t.Context(), provider, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp", AuthorizationServerMetadataURL: metadataURL})
	if err != nil || result != oauth.OAuthRedirect {
		t.Fatalf("authorize = %q, %v", result, err)
	}
	if got := provider.authorizationURL.Scheme + "://" + provider.authorizationURL.Host + provider.authorizationURL.Path; got != origin+"/idp/authorize" {
		t.Fatalf("authorization endpoint = %s", got)
	}
	if provider.discovery != cached {
		t.Fatalf("discovery state was replaced: %#v", provider.discovery)
	}
	missing, _ := url.Parse(origin + "/missing.json")
	_, err = oauth.AuthorizeMcp(t.Context(), provider, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp", AuthorizationServerMetadataURL: missing})
	if err == nil || err.Error() != "HTTP 404 loading authorization server metadata from "+origin+"/missing.json" {
		t.Fatalf("missing document: err = %v", err)
	}
}

// scopedProvider gives the test provider a client metadata scope.
type scopedProvider struct {
	*testOAuthProvider
	scope string
}

func (p *scopedProvider) ClientMetadata() oauth.OAuthClientMetadata {
	metadata := p.testOAuthProvider.ClientMetadata()
	metadata.Scope = p.scope
	return metadata
}
