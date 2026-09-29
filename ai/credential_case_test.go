package ai

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi AuthStorage reads exact JSON object properties: KEY/TYPE remain provider-owned metadata and cannot replace key/type.
func TestCredentialFieldsRemainCaseSensitive(t *testing.T) {
	for _, metadata := range []string{`"KEY":"metadata"`, `"TYPE":"oauth"`, `"Key":{"opaque":true}`, `"Key":"metadata"`} {
		t.Run(metadata, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			raw := `{"provider":{"type":"api_key","key":"stored",` + metadata + `}}`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			credential, err := store.Read(t.Context(), "provider")
			if err != nil || credential == nil || credential.Type != CredentialAPIKey || credential.Key != "stored" {
				t.Fatalf("metadata replaced credential identity: %+v, %v", credential, err)
			}
			auth, err := ResolveProviderAuth(t.Context(), "provider", ProviderAuth{APIKey: EnvAPIKeyAuth("key", "UNUSED_AUTH_KEY")}, store, testAuthContext(nil), AuthResolutionOverrides{})
			if err != nil || auth == nil || auth.Auth.APIKey != "stored" {
				t.Fatalf("resolved auth=%+v error=%v", auth, err)
			}
			if err := store.Delete(t.Context(), "absent"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("persistence changed exact properties: %s want=%s", data, raw)
			}
		})
	}
}

func TestCredentialJSONProjectionUsesExactFieldNames(t *testing.T) {
	base := `{"type":"api_key","key":"stored","apiKey":"legacy","env":{"REGION":"real"},"refresh":"refresh","access":"access","expires":123,"projectId":"project","accountId":"account","enterpriseUrl":"enterprise","scope":"scope","availableModelIds":["model"],"gatewayConfig":{"region":"real"}}`
	encode := func(value any) ([]byte, error) {
		if raw, ok := value.(*rawCredential); ok {
			return json.Marshal(raw.normalize())
		}
		return json.Marshal(value)
	}
	for _, tc := range []struct {
		name     string
		newValue func() any
	}{
		{"credential", func() any { return new(Credential) }},
		{"raw credential", func() any { return new(rawCredential) }},
		{"OAuth credential", func() any { return new(OAuthCredentials) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reference := tc.newValue()
			if err := json.Unmarshal([]byte(base), reference); err != nil {
				t.Fatal(err)
			}
			encoded, err := encode(reference)
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &want); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"type", "key", "apiKey", "env", "refresh", "access", "expires", "projectId", "accountId", "enterpriseUrl", "scope", "availableModelIds", "gatewayConfig"} {
				t.Run(field, func(t *testing.T) {
					upper := strings.ToUpper(field)
					data := strings.TrimSuffix(base, "}") + `,"` + upper + `":{"opaque":true}}`
					actual := tc.newValue()
					if err := json.Unmarshal([]byte(data), actual); err != nil {
						t.Fatalf("opaque %s metadata rejected: %v", upper, err)
					}
					encoded, err := encode(actual)
					if err != nil {
						t.Fatal(err)
					}
					var got map[string]json.RawMessage
					if err := json.Unmarshal(encoded, &got); err != nil {
						t.Fatal(err)
					}
					if string(got[upper]) != `{"opaque":true}` {
						t.Fatalf("opaque field lost: %s", encoded)
					}
					delete(got, upper)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s changed typed fields: %s", upper, encoded)
					}
				})
			}
		})
	}
}
