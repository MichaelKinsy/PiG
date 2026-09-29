package ai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Pi auth-storage.ts:477-486 commits deletion under the cancellable storage lock.
func TestAuthStorageDeleteCancellationPreservesCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("provider", Credential{Type: CredentialAPIKey, Key: "stored"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("cancel deletion after acquiring lock")
	read := readAuthFile
	readAuthFile = func(name string) ([]byte, error) {
		data, err := read(name)
		cancel(cause)
		return data, err
	}
	t.Cleanup(func() { readAuthFile = read })
	credentials := NewRuntimeCredentials(store)
	credentials.SetRuntimeAPIKey("provider", "runtime")
	if err := credentials.Delete(ctx, "provider"); !errors.Is(err, cause) {
		t.Fatalf("Delete = %v; want cancellation cause", err)
	}
	if !credentials.HasRuntimeAPIKey("provider") {
		t.Fatal("cancelled deletion cleared runtime key")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("cancelled deletion changed file: %s, %v", after, err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deletion retained file lock: %v", err)
	}
}

// Pi models.ts:logout delegates to CredentialStore.delete; the same contract serves file, memory, and read-only stores.
func TestModelsLogoutUsesCredentialStoreDeletion(t *testing.T) {
	for _, kind := range []string{"file", "memory", "read-only"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			file, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			stored := Credential{Type: CredentialOAuth, Access: "access", Refresh: "refresh", Expires: 123}
			other := Credential{Type: CredentialAPIKey, Key: "other"}
			var store CredentialStore = file
			if kind == "memory" {
				store = NewInMemoryAuthStorage(nil)
			}
			for id, credential := range map[string]Credential{"provider": stored, "unrelated": other} {
				if _, err := store.Modify(t.Context(), id, func(*Credential) (*Credential, error) { return &credential, nil }); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "read-only" {
				store = NewReadOnlyAuthStorage(path)
			}
			credentials := NewRuntimeCredentials(store)
			credentials.SetRuntimeAPIKey("provider", "runtime")
			models := CreateModels(CreateModelsOptions{Credentials: credentials})
			t.Cleanup(models.Close)
			err = models.Logout(t.Context(), "provider")
			if kind == "read-only" {
				if err == nil || err.Error() != "Credential store delete failed for provider: Read-only credential storage cannot modify auth.json" {
					t.Fatalf("read-only logout = %v", err)
				}
				if !credentials.HasRuntimeAPIKey("provider") {
					t.Fatal("failed deletion cleared runtime key")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got, err := credentials.Read(t.Context(), "provider"); got != nil || err != nil {
					t.Fatalf("credential after logout = %+v, %v", got, err)
				}
				if err := models.Logout(t.Context(), "absent"); err != nil {
					t.Fatalf("logout absent provider = %v", err)
				}
			}
			if got, err := store.Read(t.Context(), "unrelated"); err != nil || !reflect.DeepEqual(got, &other) {
				t.Fatalf("unrelated credential = %+v, %v", got, err)
			}
		})
	}
}
