package ai

import (
	"os"
	"path/filepath"
	"testing"
)

// AuthStorage writes JSON.stringify(data, null, 2) (auth-storage.ts:358,467,479), which leaves <, > and & unescaped. A
// command-backed key such as `!op read x && echo <y>` must reach auth.json as written. Node:
// JSON.stringify({ "provider-x": { type: "api_key", key: "!op read x && echo <y>" } }, null, 2).
func TestAuthStorageWritesJSONStringifyText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("provider-x", Credential{Type: CredentialAPIKey, Key: "!op read x && echo <y>"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\n  \"provider-x\": {\n    \"type\": \"api_key\",\n    \"key\": \"!op read x && echo <y>\"\n  }\n}"
	if string(got) != want {
		t.Fatalf("auth.json = %q\nwant %q", got, want)
	}
}
