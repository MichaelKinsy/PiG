package ai

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

func firstHalfAuthSet(ctx context.Context, store CredentialStore, provider, key string) error {
	_, err := store.Modify(ctx, provider, func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: key}, nil
	})
	return err
}

func firstHalfAuthDisk(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, data, want)
}

// Cases 1-13 of packages/coding-agent/test/auth-storage.test.ts. Cases 5 and 6 use TestAuthStorageCoalescesReloadReadsUpstream and TestAuthStorageCoalescedReloadSurvivesReaderCancellationUpstream with their acquisition/ownership barriers.
func TestAuthStorageFirstHalfUpstream(t *testing.T) {
	// Pi :27.
	t.Run("reads and resolves stored API-key credentials", func(t *testing.T) {
		t.Setenv("TEST_AUTH_STORAGE_KEY", "environment-key")
		store, _ := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"$TEST_AUTH_STORAGE_KEY"}}`)
		authReloadRead(t, store, "anthropic", &Credential{Type: CredentialAPIKey, Key: "environment-key"})
	})
	// Pi :40.
	t.Run("resolves command-backed API-key credentials", func(t *testing.T) {
		store, _ := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"!printf 'command-key'"}}`)
		authReloadRead(t, store, "anthropic", &Credential{Type: CredentialAPIKey, Key: "command-key"})
	})
	// Pi :46.
	t.Run("returns OAuth credentials unchanged", func(t *testing.T) {
		credential := Credential{Type: CredentialOAuth, Access: "access-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Minute).UnixMilli()}
		store := NewInMemoryAuthStorage(map[string]Credential{"anthropic": credential})
		authReloadRead(t, store, "anthropic", &credential)
	})
	// Pi :57. An ambient value makes the scoped override independently distinguishing.
	t.Run("credential-scoped env takes precedence and remains inspectable", func(t *testing.T) {
		t.Setenv("SCOPED_KEY", "ambient")
		store, _ := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"$SCOPED_KEY","env":{"SCOPED_KEY":"scoped-value","REGION":"test-region"}}}`)
		authReloadRead(t, store, "anthropic", &Credential{Type: CredentialAPIKey, Key: "scoped-value", Env: map[string]string{"SCOPED_KEY": "scoped-value", "REGION": "test-region"}})
	})
	// Pi :142,148 use the same win32 exclusion for POSIX modes.
	for _, existing := range []bool{false, true} {
		name := "creates new auth files with owner-only permissions"
		if existing {
			name = "preserves the mode of an existing auth file"
		}
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("upstream excludes POSIX file modes on win32")
			}
			path := filepath.Join(t.TempDir(), "auth.json")
			want := os.FileMode(0o600)
			if existing {
				authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"old"}}`)
				if err := os.Chmod(path, 0o660); err != nil {
					t.Fatal(err)
				}
				want = 0o660
			}
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if existing {
				if err := firstHalfAuthSet(t.Context(), store, "anthropic", "new"); err != nil {
					t.Fatal(err)
				}
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != want {
				t.Fatalf("mode=%o want=%o", info.Mode().Perm(), want)
			}
		})
	}
	// Pi :158.
	t.Run("modify persists a credential while preserving unrelated external edits", func(t *testing.T) {
		store, path := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"old"}}`)
		authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"old"},"openai":{"type":"api_key","key":"external"}}`)
		if err := firstHalfAuthSet(t.Context(), store, "anthropic", "new"); err != nil {
			t.Fatal(err)
		}
		firstHalfAuthDisk(t, path, `{"anthropic":{"type":"api_key","key":"new"},"openai":{"type":"api_key","key":"external"}}`)
	})
	// Pi :174.
	t.Run("modify with undefined leaves the current credential unchanged", func(t *testing.T) {
		store, _ := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
		got, err := store.Modify(t.Context(), "anthropic", func(*Credential) (*Credential, error) { return nil, nil })
		want := &Credential{Type: CredentialAPIKey, Key: "stored"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("modify=%#v,%v want=%#v", got, err, want)
		}
		authReloadRead(t, store, "anthropic", want)
	})
	// Pi :181.
	t.Run("serializes concurrent modifications", func(t *testing.T) {
		first, path := authReloadFile(t, `{}`)
		second, err := NewAuthStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		var workers sync.WaitGroup
		for _, item := range []struct {
			store CredentialStore
			id    string
		}{{first, "anthropic"}, {second, "openai"}} {
			workers.Go(func() {
				if err := firstHalfAuthSet(t.Context(), item.store, item.id, item.id+"-key"); err != nil {
					t.Error(err)
				}
			})
		}
		workers.Wait()
		firstHalfAuthDisk(t, path, `{"anthropic":{"type":"api_key","key":"anthropic-key"},"openai":{"type":"api_key","key":"openai-key"}}`)
	})
	// Pi :195.
	t.Run("delete removes one credential while preserving others", func(t *testing.T) {
		store, path := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"anthropic-key"},"openai":{"type":"api_key","key":"openai-key"}}`)
		authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"anthropic-key"},"openai":{"type":"api_key","key":"openai-key"},"google":{"type":"api_key","key":"external-key"}}`)
		if err := store.Delete(t.Context(), "anthropic"); err != nil {
			t.Fatal(err)
		}
		got, err := store.List(t.Context())
		want := []CredentialInfo{{ProviderID: "openai", Type: CredentialAPIKey}, {ProviderID: "google", Type: CredentialAPIKey}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("list=%v,%v want=%v", got, err, want)
		}
		authReloadRead(t, store, "anthropic", nil)
		authReloadRead(t, store, "openai", &Credential{Type: CredentialAPIKey, Key: "openai-key"})
		authReloadRead(t, store, "google", &Credential{Type: CredentialAPIKey, Key: "external-key"})
	})
	// Pi :216.
	t.Run("in-memory storage implements the same credential-store behavior", func(t *testing.T) {
		store := NewInMemoryAuthStorage(map[string]Credential{"anthropic": {Type: CredentialAPIKey, Key: "initial"}})
		authReloadRead(t, store, "anthropic", &Credential{Type: CredentialAPIKey, Key: "initial"})
		if err := firstHalfAuthSet(t.Context(), store, "anthropic", "updated"); err != nil {
			t.Fatal(err)
		}
		authReloadRead(t, store, "anthropic", &Credential{Type: CredentialAPIKey, Key: "updated"})
		if err := store.Delete(t.Context(), "anthropic"); err != nil {
			t.Fatal(err)
		}
		got, err := store.List(t.Context())
		if err != nil || !reflect.DeepEqual(got, []CredentialInfo{}) {
			t.Fatalf("list=%v,%v want=[]", got, err)
		}
	})
}
