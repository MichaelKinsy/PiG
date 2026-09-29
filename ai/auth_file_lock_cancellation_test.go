package ai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// Observe admission at AuthStorage.Delete, after Models and RuntimeCredentials perform their own cancellation checks.
type authDeleteAdmissionStore struct {
	*AuthStorage
	checked chan struct{}
}

func (s authDeleteAdmissionStore) Delete(ctx context.Context, provider string) error {
	return s.AuthStorage.Delete(&credentialAdmissionContext{Context: ctx, checked: s.checked}, provider)
}

// Pi 0.87.1 packages/coding-agent/src/core/auth-storage.ts:116-146,157-200,449-483:
// file operations acquire the cancellable file lock directly, even on the same storage instance as an active async modifier.
// Related upstream tests: packages/coding-agent/test/auth-storage.test.ts:293-312,333-389.
func TestAuthStorageFileLockWaitCancellation(t *testing.T) {
	for _, operation := range []string{"delete", "read", "list", "modify", "logout"} {
		t.Run(operation, func(t *testing.T) {
			store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			stored := Credential{Type: CredentialOAuth, Access: "stored", Refresh: "refresh", Expires: 123}
			if err := store.Set("provider", stored); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(store.Path())
			if err != nil {
				t.Fatal(err)
			}
			checked := make(chan struct{})
			credentials := NewRuntimeCredentials(authDeleteAdmissionStore{AuthStorage: store, checked: checked})
			credentials.SetRuntimeAPIKey("provider", "runtime")
			models := CreateModels(CreateModelsOptions{Credentials: credentials})
			t.Cleanup(models.Close)

			started, release := make(chan struct{}), make(chan struct{})
			first := make(chan error, 1)
			other := Credential{Type: CredentialAPIKey, Key: "committed"}
			go func() {
				_, err := store.Modify(t.Context(), "other", func(*Credential) (*Credential, error) {
					close(started)
					<-release
					return &other, nil
				})
				first <- err
			}()
			<-started

			base, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			ctx := &credentialAdmissionContext{Context: base, checked: checked}
			cause := errors.New("cancel waiting credential operation")
			result := make(chan error, 1)
			var called atomic.Bool
			go func() {
				var err error
				switch operation {
				case "delete":
					err = store.Delete(ctx, "provider")
				case "read":
					_, err = store.Read(ctx, "provider")
				case "list":
					_, err = store.List(ctx)
				case "modify":
					_, err = store.Modify(ctx, "provider", func(*Credential) (*Credential, error) {
						called.Store(true)
						return &Credential{Type: CredentialAPIKey, Key: "cancelled"}, nil
					})
				case "logout":
					err = models.Logout(base, "provider")
				}
				result <- err
			}()
			<-checked
			cancel(cause)
			returned := false
			select {
			case err := <-result:
				returned = true
				if !errors.Is(err, cause) {
					t.Errorf("%s = %v, want cancellation cause", operation, err)
				}
			case <-time.After(time.Second): // Deadlock watchdog, not scheduling: the modifier remains blocked until explicitly released below.
				t.Errorf("%s cancellation waited for the active Modify callback", operation)
			}
			if !credentials.HasRuntimeAPIKey("provider") {
				t.Error("cancelled operation cleared the runtime key")
			}
			if after, err := os.ReadFile(store.Path()); err != nil || string(after) != string(before) {
				t.Errorf("cancelled operation changed file: %s, %v", after, err)
			}
			if info, err := os.Stat(store.Path() + ".lock"); err != nil || !info.IsDir() {
				t.Errorf("waiting caller removed active modifier's lock: %v", err)
			}

			close(release)
			if err := <-first; err != nil {
				t.Errorf("active Modify = %v", err)
			}
			if !returned {
				if err := <-result; !errors.Is(err, cause) {
					t.Errorf("%s after release = %v, want cancellation cause", operation, err)
				}
			}
			if called.Load() {
				t.Error("cancelled modifier ran after lock release")
			}
			for provider, want := range map[string]Credential{"provider": stored, "other": other} {
				if got, err := store.Read(t.Context(), provider); err != nil || !reflect.DeepEqual(got, &want) {
					t.Errorf("read(%s) = %+v, %v; want %+v", provider, got, err, want)
				}
			}
			if _, err := os.Stat(store.Path() + ".lock"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("completed operations retained file lock: %v", err)
			}
			if err := store.Delete(t.Context(), "provider"); err != nil {
				t.Errorf("subsequent deletion = %v", err)
			}
		})
	}
}
