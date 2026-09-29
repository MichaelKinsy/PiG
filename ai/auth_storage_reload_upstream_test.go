package ai

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// Pi auth-storage.test.ts:72 coalesces one async lock acquisition for each changed revision, including across instances of the first auth path.
func TestAuthStorageCoalescesReloadReadsUpstream(t *testing.T) {
	isolateAuthReloadState(t)
	first, path := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"old"}}`)
	second, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	var reads, locks atomic.Int32
	originalAcquire := acquireAuthFileLock
	acquireAuthFileLock = func(ctx context.Context, name string) (*pilock.Lock, error) {
		locks.Add(1)
		return originalAcquire(ctx, name)
	}
	t.Cleanup(func() { acquireAuthFileLock = originalAcquire })
	originalRead := readAuthFile
	readAuthFile = func(name string) ([]byte, error) {
		if name == path {
			reads.Add(1)
		}
		return originalRead(name)
	}
	t.Cleanup(func() { readAuthFile = originalRead })
	authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"new"},"openai":{"type":"api_key","key":"openai-key"}}`)
	var wg sync.WaitGroup
	wg.Go(func() { authReloadRead(t, first, "anthropic", &Credential{Type: CredentialAPIKey, Key: "new"}) })
	wg.Go(func() { authReloadRead(t, second, "openai", &Credential{Type: CredentialAPIKey, Key: "openai-key"}) })
	wg.Go(func() {
		got, err := first.List(t.Context())
		want := []CredentialInfo{{ProviderID: "anthropic", Type: CredentialAPIKey}, {ProviderID: "openai", Type: CredentialAPIKey}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("list=%v,%v", got, err)
		}
	})
	wg.Wait()
	if reads.Load() != 1 || locks.Load() != 1 {
		t.Fatalf("reload reads=%d locks=%d; want one shared acquisition/read", reads.Load(), locks.Load())
	}
	authReloadRead(t, second, "anthropic", &Credential{Type: CredentialAPIKey, Key: "new"})
	if reads.Load() != 1 {
		t.Fatal("cached read reread the file")
	}
	otherPath := filepath.Join(filepath.Dir(path), "other-auth.json")
	authReloadWrite(t, otherPath, `{"other":{"type":"api_key","key":"other-key"}}`)
	otherFirst, err := NewAuthStorage(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	otherSecond, err := NewAuthStorage(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	authReloadRead(t, otherFirst, "other", &Credential{Type: CredentialAPIKey, Key: "other-key"})
	authReloadRead(t, otherSecond, "other", &Credential{Type: CredentialAPIKey, Key: "other-key"})
	if _, err := otherFirst.List(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 {
		t.Fatal("other path touched shared reload")
	}
	if locks.Load() != 1 {
		t.Fatalf("unchanged other paths acquired async locks: %d", locks.Load())
	}
	third, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"newest"}}`)
	wg.Go(func() { authReloadRead(t, first, "anthropic", &Credential{Type: CredentialAPIKey, Key: "newest"}) })
	wg.Go(func() { authReloadRead(t, third, "anthropic", &Credential{Type: CredentialAPIKey, Key: "newest"}) })
	wg.Wait()
	if reads.Load() != 2 || locks.Load() != 2 {
		t.Fatalf("reads=%d locks=%d want 2 revisions", reads.Load(), locks.Load())
	}
}

func authReloadFile(t *testing.T, raw string) (*AuthStorage, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if raw != "" {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}
func authReloadWrite(t *testing.T, path, raw string) {
	t.Helper()
	previous, statErr := os.Stat(path)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	// Give each fixture write a distinct revision even on a coarse filesystem clock; no time budget or polling interval changes.
	if statErr == nil {
		stamp := previous.ModTime().Add(time.Second)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}
func authReloadRead(t *testing.T, s CredentialStore, id string, want *Credential) {
	t.Helper()
	got, err := s.Read(t.Context(), id)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("read(%s)=%#v,%v want=%#v", id, got, err, want)
	}
}
