package oauth_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// RFC 7591 client metadata (packages/mcp/src/oauth.ts OAuthClientMetadata): dynamic registration sends every member the
// client configured, under its RFC member name, and omits the ones it did not.
// Pi: packages/mcp/src/oauth/types.ts:52 (client_uri)
// Pi: packages/mcp/src/oauth/types.ts:53 (logo_uri)
// Pi: packages/mcp/src/oauth/types.ts:55 (contacts)
// Pi: packages/mcp/src/oauth/types.ts:56 (tos_uri)
// Pi: packages/mcp/src/oauth/types.ts:57 (policy_uri)
// Pi: packages/mcp/src/oauth/types.ts:58 (jwks_uri)
// Pi: packages/mcp/src/oauth/types.ts:59 (jwks)
// Pi: packages/mcp/src/oauth/types.ts:60 (software_id)
// Pi: packages/mcp/src/oauth/types.ts:61 (software_version)
// Pi: packages/mcp/src/oauth/types.ts:62 (software_statement)
// mutation-checked: renaming the json tag of any listed member fails it
// RFC 7591 client metadata (packages/mcp/src/oauth/types.ts:41 OAuthClientMetadata): dynamic registration sends every member
// the client configured, under its RFC member name, and omits the ones it did not (registerClient serializes the metadata,
// packages/mcp/src/oauth/flow.ts:255).
func TestMCPOAuthRegistrationSendsEveryConfiguredClientMetadataMember(t *testing.T) {
	var body map[string]any
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if err := json.Unmarshal([]byte(readAll(r)), &body); err != nil {
			t.Error(err)
		}
		body["client_id"] = "client"
		jsonResponse(w, http.StatusCreated, body)
	})
	jwks := json.RawMessage(`{"keys":[{"kty":"oct","k":"a"}]}`)
	_, err := oauth.RegisterClient(t.Context(), origin, oauth.RegisterClientOptions{
		ClientMetadata: oauth.OAuthClientMetadata{
			RedirectURIs: []string{"https://app.example/cb"}, ClientURI: "https://app.example", LogoURI: "https://app.example/logo.png",
			Contacts: []string{"ops@app.example"}, TosURI: "https://app.example/tos", PolicyURI: "https://app.example/policy",
			JwksURI: "https://app.example/jwks.json", SoftwareID: "sw", SoftwareVersion: "1.2.3", SoftwareStatement: "stmt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"redirect_uris": []any{"https://app.example/cb"}, "application_type": "web", "client_uri": "https://app.example",
		"logo_uri": "https://app.example/logo.png", "contacts": []any{"ops@app.example"}, "tos_uri": "https://app.example/tos",
		"policy_uri": "https://app.example/policy", "jwks_uri": "https://app.example/jwks.json", "software_id": "sw",
		"software_version": "1.2.3", "software_statement": "stmt", "client_id": "client",
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("registration body = %v, want %v", body, want)
	}

	var decoded oauth.OAuthClientMetadata
	if err := json.Unmarshal([]byte(`{"redirect_uris":["https://a/cb"],"jwks":{"keys":[]}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.Jwks) != `{"keys":[]}` {
		t.Fatalf("jwks = %s", decoded.Jwks)
	}
	if raw, _ := json.Marshal(oauth.OAuthClientMetadata{RedirectURIs: []string{"x"}, Jwks: jwks}); string(raw) != `{"redirect_uris":["x"],"jwks":{"keys":[{"kty":"oct","k":"a"}]}}` {
		t.Fatalf("encoded jwks = %s", raw)
	}
}
