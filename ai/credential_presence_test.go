package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func BenchmarkCredentialOptionalProjection(b *testing.B) {
	for _, count := range []int{0, 8, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			fields := map[string]any{"type": "oauth", "access": "token", "refresh": "r", "expires": 42, "projectId": nil, "scope": ""}
			for i := range count {
				fields[fmt.Sprint("extra", i)] = map[string]any{"id": "provider", "enabled": false}
			}
			data, err := json.Marshal(fields)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				var credential Credential
				if err := json.Unmarshal(data, &credential); err != nil {
					b.Fatal(err)
				}
				if _, err := json.Marshal(credential); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Pi auth/types.ts:24-33 permits provider-owned OAuth fields of any JSON type; auth-storage.ts:441-470 reads and rewrites them unchanged.
func TestOAuthCredentialProjectionPreservesOptionalPresence(t *testing.T) {
	for _, payload := range []string{
		`{"refresh":"r","access":"a","expires":42}`,
		`{"refresh":"r","access":"a","expires":42,"projectId":"","accountId":null,"scope":"","enterpriseUrl":null,"nullable":null,"disabled":false}`,
		`{"refresh":"r","access":"a","expires":42,"projectId":{"id":"p"},"accountId":false,"scope":0,"enterpriseUrl":[],"env":false,"key":null}`,
	} {
		t.Run(payload, func(t *testing.T) {
			var oauth OAuthCredentials
			if err := json.Unmarshal([]byte(payload), &oauth); err != nil {
				t.Fatal(err)
			}
			assertCredentialPresence(t, oauth, payload)
			stored, err := credentialFromOAuth(oauth)
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(payload), &want); err != nil {
				t.Fatal(err)
			}
			want["type"] = "oauth"
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			assertCredentialPresence(t, stored, string(data))
			assertCredentialPresence(t, credentialToOAuth(stored), payload)
		})
	}
}

func assertCredentialPresence(t *testing.T, value any, wantJSON string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("credential = %s, want %s", data, wantJSON)
	}
}

// Pi's mutable and read-only stores preserve provider fields; read-only validation only checks the OAuth discriminator and required tokens/expiry.
func TestAuthStoresPreserveOAuthPresenceThroughRewrite(t *testing.T) {
	for _, payload := range []string{
		`{"type":"oauth","access":"old","refresh":"r","expires":42,"projectId":"","accountId":null,"scope":"","enterpriseUrl":null,"nullable":null,"disabled":false}`,
		`{"type":"oauth","access":"old","refresh":"r","expires":42,"projectId":{"id":"p"},"accountId":false,"scope":0,"enterpriseUrl":[],"env":false,"key":null}`,
	} {
		t.Run(payload, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, []byte(`{"custom":`+payload+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			readonly := NewReadOnlyAuthStorage(path)
			credential, err := readonly.Read(t.Context(), "custom")
			if err != nil {
				t.Fatal(err)
			}
			assertCredentialPresence(t, credential, payload)
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			credential, err = store.Read(t.Context(), "custom")
			if err != nil {
				t.Fatal(err)
			}
			assertCredentialPresence(t, credential, payload)
			_, err = store.Modify(t.Context(), "custom", func(current *Credential) (*Credential, error) { current.Access = "new"; return current, nil })
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(payload), &want); err != nil {
				t.Fatal(err)
			}
			want["access"] = "new"
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			credential, err = reopened.Read(t.Context(), "custom")
			if err != nil {
				t.Fatal(err)
			}
			assertCredentialPresence(t, credential, string(data))
		})
	}
}
