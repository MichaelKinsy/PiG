package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Pi Credential's OAuth shape includes arbitrary provider-owned fields. Its
// store clones and persists them when the provider rotates only access/expiry.
func TestCredentialStoreRetainsNativeProviderFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"native":{"type":"oauth","access":"old","refresh":"r","expires":1,"account":{"id":"native-account"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.Read(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	account, ok := value["account"].(map[string]any)
	if !ok || account["id"] != "native-account" {
		t.Fatalf("lost provider field: %s", raw)
	}
	if _, err := store.Modify(t.Context(), "native", func(current *Credential) (*Credential, error) { current.Access = "new"; return current, nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["native"]["access"] != "new" || stored["native"]["account"].(map[string]any)["id"] != "native-account" {
		t.Fatalf("rotation lost provider fields: %s", data)
	}
}
