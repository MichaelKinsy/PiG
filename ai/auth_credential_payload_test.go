package ai

// pi: packages/coding-agent/src/core/auth-storage.ts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi auth-storage.ts parseStorageData/read/modify/delete retains the complete JSON string identity, not only the outer provider name.
func TestPairReviewCredentialPayloadUTF16Identity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := `{"ordinary":{"type":"api_key","key":"raw-\ud800","env":{"\ud800":"one","\ufffd":"two"},"\ud800":"opaque-one","\ufffd":"opaque-two"}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	high := jsstring.FromUTF16([]uint16{0xd800})
	ro, err := NewReadOnlyAuthStorage(path).Read(t.Context(), "ordinary")
	if err != nil || ro == nil {
		t.Fatalf("readonly=%#v err=%v", ro, err)
	}
	rw, ok, err := store.GetRaw("ordinary")
	if err != nil || !ok {
		t.Fatalf("writable=%#v found=%v err=%v", rw, ok, err)
	}
	if rw.Key != ro.Key || rw.Key != "raw-"+high {
		t.Errorf("key units: writable=%x readonly=%x expected=%x", jsstring.ToUTF16(rw.Key), jsstring.ToUTF16(ro.Key), jsstring.ToUTF16("raw-"+high))
	}
	if rw.Env[high] != "one" || rw.Env["\ufffd"] != "two" {
		t.Errorf("provider env loses distinct keys: %#v", rw.Env)
	}
	if string(rw.Extra[high]) != `"opaque-one"` || string(rw.Extra["\ufffd"]) != `"opaque-two"` {
		t.Errorf("provider-owned metadata loses distinct keys: %#v", rw.Extra)
	}
	if err := store.Delete(t.Context(), "absent"); err != nil {
		t.Fatal(err)
	}
	after, err := NewReadOnlyAuthStorage(path).Read(t.Context(), "ordinary")
	if err != nil || after == nil {
		t.Fatalf("reopened=%#v err=%v", after, err)
	}
	if after.Key != "raw-"+high || after.Env[high] != "one" || after.Env["\ufffd"] != "two" {
		data, _ := os.ReadFile(path)
		t.Errorf("no-op deletion corrupts opaque payload: %s", data)
	}
}

func TestCredentialCustomJSONRetainsUTF16Payload(t *testing.T) {
	high, low := jsstring.FromUTF16([]uint16{0xd800}), jsstring.FromUTF16([]uint16{0xdc00})
	extra := map[string]json.RawMessage{
		high: json.RawMessage(`"\udc00"`), "\ufffd": json.RawMessage(`"replacement"`),
		"nested": json.RawMessage(`{"\ud800":"high","\ufffd":"replacement"}`),
	}
	for _, credential := range []Credential{
		{Type: CredentialAPIKey, Key: "raw-" + high, Env: map[string]string{high: low, "\ufffd": "replacement"}, Extra: extra},
		{Type: CredentialOAuth, Access: high, Refresh: low, ProjectID: "project-" + high, AccountID: "account-" + low, EnterpriseDomain: high + ".example", Scope: low, Extra: extra},
	} {
		t.Run(string(credential.Type), func(t *testing.T) {
			// The standard outer codec must respect the custom lossless Credential marshaler and unmarshaler.
			encoded, err := json.Marshal(credential)
			if err != nil || !utf8.Valid(encoded) || !json.Valid(encoded) {
				t.Fatalf("encode=%q, %v", encoded, err)
			}
			var decoded Credential
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, credential) {
				t.Fatalf("round trip=%#v want=%#v", decoded, credential)
			}
			path := filepath.Join(t.TempDir(), "auth.json")
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set("ordinary", credential); err != nil {
				t.Fatal(err)
			}
			if err := store.Delete(t.Context(), "absent"); err != nil {
				t.Fatal(err)
			}
			reopened, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := reopened.GetRaw("ordinary")
			if err != nil || !ok {
				t.Fatalf("persisted=%#v, %v, %v", got, ok, err)
			}
			// Persistence writes JSON.stringify text (auth-storage.ts:358), which indents RawMessage fields and writes an escaped
			// U+FFFD literally; compare the JSON.stringify forms, which keep every lone surrogate distinct from U+FFFD.
			persisted, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			persistedText, persistedErr := jsonstringify.Canonicalize(persisted)
			wantText, wantErr := jsonstringify.Canonicalize(encoded)
			if persistedErr != nil || wantErr != nil || !bytes.Equal(persistedText, wantText) {
				t.Fatalf("persisted=%s, %v; want=%s, %v", persistedText, persistedErr, wantText, wantErr)
			}
		})
	}
}

func TestOAuthCredentialCustomJSONRetainsUTF16Payload(t *testing.T) {
	high, low := jsstring.FromUTF16([]uint16{0xd800}), jsstring.FromUTF16([]uint16{0xdc00})
	want := OAuthCredentials{
		Access: high, Refresh: low, ProjectID: "project-" + high, AccountID: "account-" + low, Scope: high + low,
		Extra: map[string]json.RawMessage{high: json.RawMessage(`"\udc00"`), "\ufffd": json.RawMessage(`"replacement"`)},
	}
	encoded, err := json.Marshal(want)
	if err != nil || !utf8.Valid(encoded) || !json.Valid(encoded) {
		t.Fatalf("encode=%q, %v", encoded, err)
	}
	var decoded OAuthCredentials
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	// Adjacent surrogate halves are the same UTF-16 string as their scalar encoding.
	want.Scope = jsstring.Canonical(want.Scope)
	// The decoded property order is compared separately: the store writes the discriminator after the flow's properties, as Pi's { ...credential, type: "oauth" } does.
	if wantOrder := []string{"access", "accountId", "expires", "projectId", "refresh", "scope", high, "\ufffd"}; !reflect.DeepEqual(decoded.order.keys, wantOrder) {
		t.Fatalf("decoded order=%q want %q", decoded.order.keys, wantOrder)
	}
	content := decoded
	content.order = credentialKeyOrder{}
	if !reflect.DeepEqual(content, want) {
		t.Fatalf("OAuth round trip=%#v want=%#v", decoded, want)
	}
	converted, err := credentialFromOAuth(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if converted.Type != CredentialOAuth || converted.Access != high || converted.Refresh != low || !reflect.DeepEqual(converted.Extra, want.Extra) {
		t.Fatalf("credential conversion=%#v want fields=%#v", converted, want)
	}
}
