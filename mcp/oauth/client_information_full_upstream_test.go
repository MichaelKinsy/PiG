package oauth_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// OAuthClientInformationFull is OAuthClientInformation & OAuthClientMetadata (packages/mcp/src/oauth/types.ts:72); parseClientInformation
// (types.ts:198-209) returns it with every OAuthClientMetadata member (types.ts:41-62) the server sent, plus client_id,
// client_secret, client_id_issued_at and client_secret_expires_at. The registered client and the configured client (client_id and
// client_secret only, OAuthClientInformationMixed, types.ts:73) are one Go type: the configured one writes no redirect_uris.
// mutation-checked: dropping a metadata field's json tag, dropping the issued-at number, or writing redirect_uris for a configured client fails it
func TestParseClientInformationReturnsEveryMemberOfTheFullShape(t *testing.T) {
	const response = `{"client_id":"cid","client_secret":"sec","client_id_issued_at":1700000000,"client_secret_expires_at":1800000000,` +
		`"redirect_uris":["http://localhost/cb","http://127.0.0.1/cb"],"token_endpoint_auth_method":"client_secret_basic",` +
		`"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"application_type":"native","client_name":"Pig",` +
		`"client_uri":"https://c.example","logo_uri":"https://c.example/l.png","scope":"a b","contacts":["x@y.z"],"tos_uri":"https://c.example/tos",` +
		`"policy_uri":"https://c.example/pol","jwks_uri":"https://c.example/jwks","jwks":{"keys":[]},"software_id":"sid","software_version":"1.2",` +
		`"software_statement":"stmt"}`
	info, err := oauth.ParseClientInformation([]byte(response))
	if err != nil {
		t.Fatal(err)
	}
	if info.ClientID != "cid" || info.ClientSecret != "sec" || info.ClientIDIssuedAt == nil || *info.ClientIDIssuedAt != 1700000000 ||
		info.ClientSecretExpiresAt == nil || *info.ClientSecretExpiresAt != 1800000000 {
		t.Fatalf("client identity = %+v", info)
	}
	meta := info.OAuthClientMetadata
	if !reflect.DeepEqual(meta.RedirectURIs, []string{"http://localhost/cb", "http://127.0.0.1/cb"}) || meta.TokenEndpointAuthMethod != "client_secret_basic" ||
		!reflect.DeepEqual(meta.GrantTypes, []string{"authorization_code", "refresh_token"}) || !reflect.DeepEqual(meta.ResponseTypes, []string{"code"}) ||
		meta.ApplicationType != "native" || meta.ClientName != "Pig" || meta.ClientURI != "https://c.example" || meta.LogoURI != "https://c.example/l.png" ||
		meta.Scope != "a b" || !reflect.DeepEqual(meta.Contacts, []string{"x@y.z"}) || meta.TosURI != "https://c.example/tos" ||
		meta.PolicyURI != "https://c.example/pol" || meta.JwksURI != "https://c.example/jwks" || string(meta.Jwks) != `{"keys":[]}` ||
		meta.SoftwareID != "sid" || meta.SoftwareVersion != "1.2" || meta.SoftwareStatement != "stmt" {
		t.Fatalf("metadata = %+v", meta)
	}
	if len(info.Extra) != 0 {
		t.Fatalf("extra = %v, want every member kept in its typed field", info.Extra)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(response), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("re-encoded\n got  %v\n want %v", got, want)
	}

	configured, err := json.Marshal(oauth.OAuthClientInformationMixed{ClientID: "cid", ClientSecret: "sec"})
	if err != nil {
		t.Fatal(err)
	}
	if string(configured) != `{"client_id":"cid","client_secret":"sec"}` {
		t.Fatalf("configured client = %s, want only client_id and client_secret", configured)
	}
}
