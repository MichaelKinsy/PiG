package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pi: auth-storage.ts keeps every provider's entry as parsed and writes the whole object with JSON.stringify(merged, null, 2): an entry the credential types do not describe survives an unrelated write unchanged, and a wrong-typed field in one entry does not stop the others.
func TestAuthStorageKeepsEntriesItCannotTypeAcrossWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	const initial = `{"incomplete":{"type":"oauth"},"wrongtyped":{"type":"api_key","key":5,"env":{"A":1}},"scalar":"x","good":{"type":"api_key","key":"k"}}`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Modify(context.Background(), "added", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
	}); err != nil {
		t.Fatalf("a wrong-typed entry stopped a write to another provider: %v", err)
	}
	got, _ := os.ReadFile(path)
	const want = "{\n  \"incomplete\": {\n    \"type\": \"oauth\"\n  },\n  \"wrongtyped\": {\n    \"type\": \"api_key\",\n    \"key\": 5,\n    \"env\": {\n      \"A\": 1\n    }\n  },\n  \"scalar\": \"x\",\n  \"good\": {\n    \"type\": \"api_key\",\n    \"key\": \"k\"\n  },\n  \"added\": {\n    \"type\": \"api_key\",\n    \"key\": \"new\"\n  }\n}"
	if string(got) != want {
		t.Fatalf("auth.json after an unrelated write:\n%s\nwant:\n%s", got, want)
	}
	infos, err := storage.List(context.Background())
	if err != nil || len(infos) != 5 {
		t.Fatalf("List = %v, %v; want the five providers", infos, err)
	}

	// A caller that changes a credential it read gets its change written, not the kept source.
	credential, ok, err := storage.GetRaw("incomplete")
	if err != nil || !ok {
		t.Fatalf("GetRaw(incomplete) = %v, %v, %v", credential, ok, err)
	}
	credential.Access, credential.Refresh = "tok", "ref"
	if err := storage.Set("incomplete", credential); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if !strings.Contains(string(got), `"access": "tok"`) || !strings.Contains(string(got), `"refresh": "ref"`) {
		t.Fatalf("a changed credential was written from its stale source:\n%s", got)
	}
}
