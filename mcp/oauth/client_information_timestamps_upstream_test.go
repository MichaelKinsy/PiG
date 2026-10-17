package oauth_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// parseClientInformation (packages/mcp/src/oauth/types.ts:198-209) spreads the response, then overrides client_id_issued_at and
// client_secret_expires_at with the member only when `typeof` says number (undefined otherwise), and compact() (types.ts:101-103) drops every
// undefined. A timestamp that is a numeric string, null, a boolean or an object is therefore absent from the registered client: not a typed value
// and not a member kept from the spread. A real number, an integer or a fraction, is kept.
// mutation-checked: leaving client_id_issued_at, or client_secret_expires_at, out of the members the parse overrides keeps a numeric string in
// the extra members (fails the numeric strings case); a non-number that decodes to 1 fails the booleans case.
func TestParseClientInformationKeepsOnlyNumericTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name, issued, expires string
		wantIssued            *float64
		wantExpires           *float64
	}{
		{name: "numbers", issued: `1700000000`, expires: `1800000000.5`, wantIssued: new(1700000000.0), wantExpires: new(1800000000.5)},
		{name: "numeric strings", issued: `"1700000000"`, expires: `"1800000000"`},
		{name: "null", issued: `null`, expires: `null`},
		{name: "booleans", issued: `true`, expires: `false`},
		{name: "objects", issued: `{"a":1}`, expires: `[1]`},
		{name: "mixed", issued: `5`, expires: `"7"`, wantIssued: new(5.0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := oauth.ParseClientInformation([]byte(`{"client_id":"cid","client_id_issued_at":` + tc.issued + `,"client_secret_expires_at":` + tc.expires + `}`))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(info.ClientIDIssuedAt, tc.wantIssued) || !reflect.DeepEqual(info.ClientSecretExpiresAt, tc.wantExpires) {
				t.Fatalf("issued %v expires %v, want %v and %v", numberOrNil(info.ClientIDIssuedAt), numberOrNil(info.ClientSecretExpiresAt), numberOrNil(tc.wantIssued), numberOrNil(tc.wantExpires))
			}
			encoded, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			var members map[string]any
			if err := json.Unmarshal(encoded, &members); err != nil {
				t.Fatal(err)
			}
			if _, present := members["client_id_issued_at"]; present != (tc.wantIssued != nil) {
				t.Fatalf("client_id_issued_at present = %t in %s", present, encoded)
			}
			if _, present := members["client_secret_expires_at"]; present != (tc.wantExpires != nil) {
				t.Fatalf("client_secret_expires_at present = %t in %s", present, encoded)
			}
		})
	}
}

func numberOrNil(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
