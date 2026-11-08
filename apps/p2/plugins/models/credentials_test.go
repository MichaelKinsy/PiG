package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileCredentialsReadListModifyDelete(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.json")
	writeFile(t, path, `{"b":{"type":"api_key","key":"$P2_TEST_KEY"},"a":{"type":"oauth","access":"x","refresh":"y","expires":1}}`)
	t.Setenv("P2_TEST_KEY", "resolved")
	store := NewFileCredentials(path)
	defer store.Close()

	credential, err := store.Read(ctx, "b")
	if err != nil || credential == nil || credential.Key != "resolved" {
		t.Fatalf("read b = %+v, %v", credential, err)
	}
	if credential, err := store.Read(ctx, "missing"); credential != nil || err != nil {
		t.Fatalf("read missing = %+v, %v", credential, err)
	}
	// Listing never resolves a key and keeps the file's key order.
	infos, err := store.List(ctx)
	if err != nil || len(infos) != 2 || infos[0].ProviderID != "b" || infos[1].Type != ai.CredentialOAuth {
		t.Fatalf("list = %+v, %v", infos, err)
	}
	if snapshot := store.StoredSnapshot("b"); snapshot == nil || snapshot.Key != "$P2_TEST_KEY" {
		t.Fatalf("the snapshot keeps the key unresolved: %+v", snapshot)
	}
	if store.Revision() == "" {
		t.Fatal("no revision after a read")
	}

	// A key that cannot be resolved is an error, not an empty key.
	writeFile(t, path, `{"c":{"type":"api_key","key":"$P2_TEST_MISSING_KEY"}}`)
	if _, err := store.Read(ctx, "c"); err == nil || err.Error() != "Could not resolve stored API key" {
		t.Fatalf("unresolvable key: %v", err)
	}

	updated, err := store.Modify(ctx, "d", func(current *ai.Credential) (*ai.Credential, error) {
		if current != nil {
			t.Fatalf("d is new: %+v", current)
		}
		return &ai.Credential{Type: ai.CredentialAPIKey, Key: "secret"}, nil
	})
	if err != nil || updated == nil || updated.Key != "secret" {
		t.Fatalf("modify = %+v, %v", updated, err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "\n  \"d\"") || !strings.Contains(string(content), "\"c\"") {
		t.Fatalf("saved file: %s", content)
	}
	// nil leaves the entry as it is, without a write.
	before, _ := os.ReadFile(path)
	if _, err := store.Modify(ctx, "d", func(*ai.Credential) (*ai.Credential, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a nil result wrote the file")
	}
	if err := store.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	infos, _ = store.List(ctx)
	if len(infos) != 1 || infos[0].ProviderID != "d" {
		t.Fatalf("after delete: %+v", infos)
	}
}

func TestFileCredentialsValidation(t *testing.T) {
	ctx := context.Background()
	for content, want := range map[string]string{
		`[]`:                                     "Invalid auth.json: expected object",
		`{`:                                      "Invalid auth.json syntax",
		`{"a":1}`:                                "Invalid auth.json credential",
		`{"a":{"type":"api_key","key":1}}`:       "Invalid auth.json credential",
		`{"a":{"type":"api_key","env":{"X":1}}}`: "Invalid auth.json credential",
		`{"a":{"type":"oauth","access":"x","refresh":"y"}}`: "Invalid auth.json credential",
		`{"a":{"type":"other"}}`:                            "Invalid auth.json credential",
	} {
		path := filepath.Join(t.TempDir(), "auth.json")
		writeFile(t, path, content)
		store := NewFileCredentials(path)
		// Invalid startup auth fails closed.
		if _, err := store.Read(ctx, "a"); err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", content, err, want)
		}
		store.Close()
	}
	// Once a valid file was read, an invalid one keeps the last good content.
	path := filepath.Join(t.TempDir(), "auth.json")
	writeFile(t, path, `{"a":{"type":"api_key","key":"k"}}`)
	store := NewFileCredentials(path)
	defer store.Close()
	if _, err := store.Read(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, `{ half`)
	credential, err := store.Read(ctx, "a")
	if err != nil || credential == nil || credential.Key != "k" {
		t.Fatalf("last good = %+v, %v", credential, err)
	}
	// A modification that would make the file invalid is refused and writes nothing.
	writeFile(t, path, `{"a":{"type":"api_key","key":"k"}}`)
	if _, err := store.Modify(ctx, "bad", func(*ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: "nonsense"}, nil
	}); err == nil {
		t.Fatal("an invalid credential was saved")
	}
	// An update's error is the modification's error.
	boom := errors.New("boom")
	if _, err := store.Modify(ctx, "a", func(*ai.Credential) (*ai.Credential, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("update error = %v", err)
	}
	// A closed store refuses.
	store.Close()
	if _, err := store.Read(ctx, "a"); err != nil {
		// reads fall back to the last good content once one exists
		t.Fatalf("closed read with last good: %v", err)
	}
	if err := store.Delete(ctx, "a"); err == nil {
		t.Fatal("a closed store deleted")
	}
}

func TestFileCredentialsCancelledModify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewFileCredentials(path)
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Modify(ctx, "a", func(*ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialAPIKey, Key: "k"}, nil
	}); err == nil {
		t.Fatal("a cancelled modification succeeded")
	}
	if content, _ := os.ReadFile(path); strings.Contains(string(content), `"k"`) {
		t.Fatalf("a cancelled modification wrote: %s", content)
	}
}
