package ai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type credentialContextKey struct{}

func newCredentialContextStore(t *testing.T) *AuthStorage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"custom":{"type":"api_key","key":"k","env":{"REGION":"eu"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// credentialIn wraps a context that can end in context.WithoutCancel (the lookup is never cancelled: D628) and passes one that cannot end as it is. Every context here reads the credential to completion, and a
// value a context carries (the file-read snapshot) is seen on both paths. A custom context with Done() == nil but a non-nil Err or a Deadline is not one the product makes; it is not special-cased.
func TestCredentialInReadsToCompletionUnderEveryKindOfContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer expire()
	live := t.Context()
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"background", context.Background()},
		{"value only", context.WithValue(context.Background(), credentialContextKey{}, 1)},
		{"cancelable", live},
		{"already cancelled", cancelled},
		{"deadline passed", expired},
		{"snapshot over a cancelable parent", WithFileReadSnapshot(live)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newCredentialContextStore(t)
			cred, ok, err := store.credentialIn(tc.ctx, "custom")
			if err != nil || !ok || cred.Key != "k" || cred.Env["REGION"] != "eu" {
				t.Fatalf("credentialIn = %+v, %v, %v", cred, ok, err)
			}
		})
	}
}

// The file-read snapshot is a value of the context: two reads under it stat the file once whether the context can end (wrapped) or cannot (passed as it is).
func TestCredentialInSeesTheFileReadSnapshotOnBothPaths(t *testing.T) {
	live := t.Context()
	for _, tc := range []struct {
		name   string
		parent context.Context
	}{{"cannot end", context.Background()}, {"can end", live}} {
		t.Run(tc.name, func(t *testing.T) {
			store := newCredentialContextStore(t)
			stats := countRevisionStats(t)
			ctx := WithFileReadSnapshot(tc.parent)
			for range 3 {
				if _, ok, err := store.credentialIn(ctx, "custom"); err != nil || !ok {
					t.Fatalf("credentialIn = %v, %v", ok, err)
				}
			}
			if got := stats(store.path); got != 1 {
				t.Fatalf("three reads under one snapshot statted %d times, want 1", got)
			}
		})
	}
}
