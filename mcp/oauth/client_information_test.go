package oauth_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Pi's parseClientInformation (packages/mcp/src/oauth/types.ts:198-209) spreads the whole response, so a member of an
// unexpected type leaves the others readable; selectClientAuthMethod (flow.ts:125-138) then reads
// token_endpoint_auth_method from the parsed information. A Go decode of the whole struct fails on the first ill-typed
// member and would lose every typed member with it.
func TestParseClientInformationKeepsTypedMembersBesideAnIllTypedOne(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"ill-typed contacts", `{"client_id":"c","client_secret":"s","token_endpoint_auth_method":"client_secret_post","client_name":"n","contacts":"bad"}`},
		{"ill-typed application_type", `{"application_type":5,"client_id":"c","client_secret":"s","token_endpoint_auth_method":"client_secret_post","client_name":"n"}`},
		{"partly ill-typed array", `{"client_id":"c","client_secret":"s","token_endpoint_auth_method":"client_secret_post","client_name":"n","grant_types":["a",5]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := oauth.ParseClientInformation([]byte(tc.response))
			if err != nil {
				t.Fatal(err)
			}
			if info.TokenEndpointAuthMethod != "client_secret_post" || info.ClientName != "n" {
				t.Fatalf("typed members = %q, %q; want client_secret_post, n", info.TokenEndpointAuthMethod, info.ClientName)
			}
			if info.GrantTypes != nil {
				t.Fatalf("grant_types = %q; an ill-typed array must stay raw, not decode in part", info.GrantTypes)
			}
			encoded, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.response), &want); err != nil {
				t.Fatal(err)
			}
			want["redirect_uris"] = []any{}
			if gotText, wantText := mustJSON(t, got), mustJSON(t, want); gotText != wantText {
				t.Fatalf("marshalled\n got  %s\n want %s", gotText, wantText)
			}
		})
	}
}

// JavaScript reads the exact member name; encoding/json would match CLIENT_NAME to client_name.
func TestParseClientInformationReadsExactMemberNames(t *testing.T) {
	info, err := oauth.ParseClientInformation([]byte(`{"client_id":"c","TOKEN_ENDPOINT_AUTH_METHOD":"none","Client_Name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.TokenEndpointAuthMethod != "" || info.ClientName != "" {
		t.Fatalf("typed members = %q, %q; want both empty", info.TokenEndpointAuthMethod, info.ClientName)
	}
	if string(info.Extra["TOKEN_ENDPOINT_AUTH_METHOD"]) != `"none"` || string(info.Extra["Client_Name"]) != `"x"` {
		t.Fatalf("extra = %v; want the members kept under their own names", info.Extra)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
