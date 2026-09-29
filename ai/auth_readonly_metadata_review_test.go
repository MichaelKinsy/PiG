package ai

import (
	"os"
	"path/filepath"
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi ReadOnlyAuthStorage.read retains every field in the validated credential, not a projection that deletes provider-owned fields.
func TestReadOnlyAuthStoragePreservesValidatedCredentialShape(t *testing.T) {
	for _, raw := range []string{
		`{"type":"api_key","key":"stored","KEY":"opaque","TYPE":"opaque","providerMetadata":{"z":1,"a":[true,null]},"other":17}`,
		`{"type":"api_key","key":"","env":{},"providerMetadata":false}`,
		`{"type":"oauth","access":"access","refresh":"refresh","expires":1,"env":{"REGION":null},"providerMetadata":"opaque"}`,
		`{"type":"oauth","access":"access","refresh":"refresh","expires":1,"accountId":"account","scope":"scope","env":{"REGION":"region"},"gatewayConfig":{"z":1},"availableModelIds":[],"providerMetadata":"opaque"}`,
	} {
		path := filepath.Join(t.TempDir(), "auth.json")
		if err := os.WriteFile(path, []byte(`{"provider":`+raw+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		store := NewReadOnlyAuthStorage(path)
		credential, err := store.Read(t.Context(), "provider")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(credential)
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, raw)
		metadata := credential.Extra["providerMetadata"]
		if len(metadata) == 0 {
			t.Fatal("provider metadata missing from validated credential")
		}
		metadata[0] = '!'
		again, err := store.Read(t.Context(), "provider")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err = json.Marshal(again)
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, raw)
	}
}

func TestReadOnlyAuthStorageRetainsOpaqueMetadataReview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"ordinary":{"type":"api_key","key":"stored","providerMetadata":{"account":"example"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := NewReadOnlyAuthStorage(path).Read(t.Context(), "ordinary")
	if err != nil || credential == nil {
		t.Fatalf("read=%#v, %v", credential, err)
	}
	if string(credential.Extra["providerMetadata"]) != `{"account":"example"}` {
		t.Fatalf("provider-owned metadata dropped: %#v", credential)
	}
}
