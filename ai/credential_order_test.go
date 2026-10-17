package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// auth-storage.ts:465-467 writes the credential object a login returned with JSON.stringify, which keeps the object's property order, and AuthStorage re-writes a file's credentials the same way. A credential keeps the key order of the JSON it was decoded from, so a native provider's login result (or an auth.json written by another tool) is persisted as it arrived instead of in Go's struct order.
func TestCredentialMarshalKeepsTheDecodedKeyOrder(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"api key env first", `{"env":{"A":"1"},"key":"secret","type":"api_key"}`},
		{"api key extras between", `{"zeta":1,"type":"api_key","key":"secret","alpha":{"b":2,"a":1}}`},
		{"oauth tokens first", `{"access":"a","type":"oauth","refresh":"r","expires":123,"scope":"s"}`},
		{"oauth extra first", `{"vendor":true,"type":"oauth","expires":5,"refresh":"r","access":"a"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var credential Credential
			if err := json.Unmarshal([]byte(tc.in), &credential); err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(credential)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.in {
				t.Fatalf("marshal = %s, want the decoded order %s", got, tc.in)
			}
			copied := credential
			again, _ := json.Marshal(copied)
			if string(again) != tc.in {
				t.Fatalf("a copied credential marshals %s, want %s", again, tc.in)
			}
		})
	}
}

// A credential built in Go, or decoded from Go's own canonical order, carries no recorded order: it compares equal to the constructed value and marshals in the canonical order as before.
func TestCredentialInCanonicalOrderCarriesNoRecordedOrder(t *testing.T) {
	built := Credential{Type: CredentialAPIKey, Key: "secret", Env: map[string]string{"A": "1"}}
	canonical, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{"type":"api_key","key":"secret","env":{"A":"1"}}` {
		t.Fatalf("canonical = %s", canonical)
	}
	var decoded Credential
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, built) {
		t.Fatalf("decoded = %+v, want %+v", decoded, built)
	}
}

// A key the typed projection drops (a duplicate of an extension key, an empty omitempty field) is skipped without shifting the other keys, and a key the credential gains is appended after the recorded ones.
func TestCredentialOrderSkipsAbsentKeysAndAppendsNewOnes(t *testing.T) {
	var credential Credential
	if err := json.Unmarshal([]byte(`{"env":{"A":"1"},"type":"api_key","key":"old"}`), &credential); err != nil {
		t.Fatal(err)
	}
	credential.Key = ""
	credential.Extra = map[string]json.RawMessage{"note": json.RawMessage(`"n"`)}
	got, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"env":{"A":"1"},"type":"api_key","note":"n"}` {
		t.Fatalf("marshal = %s", got)
	}
}
