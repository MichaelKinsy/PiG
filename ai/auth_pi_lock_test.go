package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// Pi 0.87.1 auth-storage.ts:76,128 takes proper-lockfile's directory lock,
// including while an asynchronous credential modifier is running.
func TestAuthStorageUsesPiLockDirectory(t *testing.T) {
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Modify(t.Context(), "probe", func(*Credential) (*Credential, error) {
		info, err := os.Stat(store.Path() + ".lock")
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			t.Error("auth lock is a file; Pi cannot observe it")
		}
		return &Credential{Type: CredentialAPIKey, Key: "probe"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path() + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock remains: %v", err)
	}
}

// Pi auth-storage.ts:24-25 applies 0600 only at creation. Replacing the inode
// loses administrator modes/ACLs and breaks a shared symlink target.
func TestAuthStoragePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode assertion; TestAuthStoragePreservesExistingACL covers the Windows DACL")
	}
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("probe", Credential{Type: CredentialAPIKey, Key: "probe"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("mode = %o, want 660", info.Mode().Perm())
	}
}

func TestAuthStorageCancelledModifierRetainsLockUntilReturn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("probe", Credential{Type: CredentialAPIKey, Key: "stored"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	started, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := store.Modify(ctx, "probe", func(*Credential) (*Credential, error) {
			close(started)
			<-finish
			return &Credential{Type: CredentialAPIKey, Key: "cancelled"}, nil
		})
		done <- err
	}()
	<-started
	cancel()
	other, err := pilock.AcquireSync(path)
	if other != nil {
		_ = other.Release()
	}
	if !errors.Is(err, pilock.ErrLocked) {
		t.Errorf("cancel released active callback's lock: %v", err)
	}
	close(finish)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("modify: %v", err)
	}
	cred, ok, err := store.GetRaw("probe")
	if err != nil || !ok || cred.Key != "stored" {
		t.Fatalf("cancelled modifier committed: %+v %v", cred, err)
	}
}

func TestAuthStorageCompromisedModifierDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("probe", Credential{Type: CredentialAPIKey, Key: "stored"}); err != nil {
		t.Fatal(err)
	}
	_, err = store.Modify(t.Context(), "probe", func(*Credential) (*Credential, error) {
		if err := os.Remove(path + ".lock"); err != nil {
			return nil, err
		}
		return &Credential{Type: CredentialAPIKey, Key: "must-not-write"}, nil
	})
	if err == nil {
		t.Fatal("compromised modifier succeeded")
	}
	cred, ok, err := store.GetRaw("probe")
	if err != nil || !ok || cred.Key != "stored" {
		t.Fatalf("compromised modifier committed: %+v %v", cred, err)
	}
}

func TestOAuthCredentialZeroFieldsRemainReadableByPi(t *testing.T) {
	data, err := json.Marshal(Credential{Type: CredentialOAuth})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	// ReadOnlyAuthStorage validates all three fields, even for expired tokens
	// (Pi auth-storage.ts:258-267).
	for _, key := range []string{"access", "refresh", "expires"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing required OAuth field %s: %s", key, data)
		}
	}
}
