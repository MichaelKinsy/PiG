package sdk

import (
	"encoding/json"
	"reflect"
	"testing"
)

// JSON.parse then JSON.stringify writes an integer above 2**53 with Number::toString digits: Node prints JSON.stringify(JSON.parse("1152921504606846976")) as 1152921504606847000 (packages/ai/src/auth/types.ts OAuthCredentials.expires is a JavaScript number).
func TestOAuthCredentialsLargeIntegerExpiresMatchesJavaScript(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"1152921504606846976", "1152921504606847000"},
		{"9007199254740993", "9007199254740992"},
		{"1700000000000", "1700000000000"},
	} {
		var creds OAuthCredentials
		if err := json.Unmarshal([]byte(`{"refresh":"r","access":"a","expires":`+tc.raw+`}`), &creds); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(creds)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if got := string(fields["expires"]); got != tc.want {
			t.Errorf("expires %s re-encoded as %s, JavaScript %s", tc.raw, got, tc.want)
		}
		var set OAuthCredentials
		millis, _ := creds.ExpiresMillis()
		set.SetExpiresMillis(millis)
		if encoded, _ := json.Marshal(set); !json.Valid(encoded) || string(mustField(t, encoded, "expires")) != tc.want {
			t.Errorf("SetExpiresMillis(%s) encoded %s, JavaScript %s", tc.raw, encoded, tc.want)
		}
	}
}

func mustField(t *testing.T, encoded []byte, key string) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	return fields[key]
}

// The six fields sdk.OAuthCredentials had before Pi's complete token object was ported keep their positions, so positional code that indexes or copies them keeps its meaning; later fields follow them.
func TestOAuthCredentialsKeepsOriginalFieldOrder(t *testing.T) {
	typ := reflect.TypeFor[OAuthCredentials]()
	for i, name := range []string{"Refresh", "Access", "Expires", "ProjectID", "AccountID", "Scope"} {
		if got := typ.Field(i).Name; got != name {
			t.Fatalf("field %d = %s, want %s", i, got, name)
		}
	}
}
