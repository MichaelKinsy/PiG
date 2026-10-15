package sdk

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi stores a credential object as JSON.parse read it and writes it with JSON.stringify (auth-storage.ts:465-467): keys and nested keys keep their order, a repeated key keeps its first position and its last value, and { ...credential, key } keeps an existing key's place and adds a new one last.
func TestCredentialKeepsKeyOrder(t *testing.T) {
	var credential Credential
	if err := json.Unmarshal([]byte(`{"type":"oauth","account":{"zone":"z1","id":"a1","tags":[{"b":1,"a":2}]},"access":"stale","type":"oauth"}`), &credential); err != nil {
		t.Fatal(err)
	}
	refreshed := credential.Clone()
	refreshed.Set("access", "fresh")
	refreshed.Set("expires", float64(5))
	got, err := json.Marshal(refreshed)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"oauth","account":{"zone":"z1","id":"a1","tags":[{"b":1,"a":2}]},"access":"fresh","expires":5}`; string(got) != want {
		t.Fatalf("refreshed = %s, want %s", got, want)
	}
	if original, _ := json.Marshal(&credential); string(original) != `{"type":"oauth","account":{"zone":"z1","id":"a1","tags":[{"b":1,"a":2}]},"access":"stale"}` {
		t.Fatalf("Clone shares its keys with the original: %s", original)
	}
	if credential.String("access") != "stale" || credential.String("account") != "" {
		t.Fatalf("String(access) = %q, String(account) = %q", credential.String("access"), credential.String("account"))
	}
}

// An absent credential arrives as null: Get finds nothing, Keys is empty and it encodes as null.
func TestNilCredentialIsAbsent(t *testing.T) {
	var params struct {
		Credential *Credential `json:"credential"`
	}
	if err := json.Unmarshal([]byte(`{"credential":null}`), &params); err != nil {
		t.Fatal(err)
	}
	if _, ok := params.Credential.Get("key"); ok || len(params.Credential.Keys()) != 0 || params.Credential.String("key") != "" {
		t.Fatalf("a null credential = %#v", params.Credential)
	}
	// { ...undefined, access: "a" } is { access: "a" }.
	refreshed := params.Credential.Clone()
	refreshed.Set("access", "a")
	if encoded, err := json.Marshal(refreshed); err != nil || string(encoded) != `{"access":"a"}` {
		t.Fatalf("a refreshed absent credential = %s, %v", encoded, err)
	}
	if encoded, err := json.Marshal(params); err != nil || string(encoded) != `{"credential":null}` {
		t.Fatalf("encoded = %s, %v", encoded, err)
	}
	var credential Credential
	if err := json.Unmarshal([]byte(`["not","an","object"]`), &credential); err == nil {
		t.Fatal("an array decoded as a credential")
	}
}
