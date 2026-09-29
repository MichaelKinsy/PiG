package ai

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestProviderLoginPreservesEmptyAndOmittedStoredKeys(t *testing.T) {
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := EnvAPIKeyAuth("Key").Login(t.Context(), AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("empty", key); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("ambient", Credential{Type: CredentialAPIKey}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"empty", "ambient"} {
		credential, ok, err := store.GetRaw(id)
		if err != nil || !ok {
			t.Fatalf("credential=%v %v %v", credential, ok, err)
		}
		raw, err := json.Marshal(credential)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		_, present := fields["key"]
		if present != (id == "empty") {
			t.Fatalf("%s round-trip=%s", id, raw)
		}
	}
}
