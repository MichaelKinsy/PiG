package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// .upstream/v0.87.1/packages/coding-agent/test/runtime-credentials.test.ts:7
func TestRuntimeCredentialsOverridesMaskStoredWithoutPersistingUpstream(t *testing.T) {
	storage := NewInMemoryAuthStorage(map[string]Credential{"anthropic": {Type: CredentialAPIKey, Key: "stored-key"}})
	credentials := NewRuntimeCredentials(storage)
	credentials.SetRuntimeAPIKey("anthropic", "runtime-key")
	assertRuntimeCredential(t, credentials, "runtime-key")
	assertRuntimeCredential(t, storage, "stored-key")
	credentials.RemoveRuntimeAPIKey("anthropic")
	assertRuntimeCredential(t, credentials, "stored-key")
}

// .upstream/v0.87.1/packages/coding-agent/test/runtime-credentials.test.ts:19
func TestRuntimeCredentialsEnumerationMergesWithoutKeysUpstream(t *testing.T) {
	storage := NewInMemoryAuthStorage(map[string]Credential{"anthropic": {Type: CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}})
	credentials := NewRuntimeCredentials(storage)
	credentials.SetRuntimeAPIKey("anthropic", "runtime-key")
	credentials.SetRuntimeAPIKey("openai", "other-runtime-key")
	got, err := credentials.List(t.Context())
	want := []CredentialInfo{{ProviderID: "anthropic", Type: CredentialAPIKey}, {ProviderID: "openai", Type: CredentialAPIKey}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %#v, %v", got, err)
	}
}

type runtimeCredentialStoreProbe struct {
	*InMemoryAuthStorage
	received    []context.Context
	deleteError error
}

func (s *runtimeCredentialStoreProbe) Read(ctx context.Context, _ string) (*Credential, error) {
	s.received = append(s.received, ctx)
	return nil, nil
}
func (s *runtimeCredentialStoreProbe) List(ctx context.Context) ([]CredentialInfo, error) {
	s.received = append(s.received, ctx)
	return []CredentialInfo{}, nil
}
func (s *runtimeCredentialStoreProbe) Modify(ctx context.Context, _ string, _ func(*Credential) (*Credential, error)) (*Credential, error) {
	s.received = append(s.received, ctx)
	return nil, nil
}
func (s *runtimeCredentialStoreProbe) Delete(ctx context.Context, _ string) error {
	s.received = append(s.received, ctx)
	return s.deleteError
}

// .upstream/v0.87.1/packages/coding-agent/test/runtime-credentials.test.ts:33
func TestRuntimeCredentialsForwardsOperationSignalsUpstream(t *testing.T) {
	storage := &runtimeCredentialStoreProbe{}
	credentials := NewRuntimeCredentials(storage)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := credentials.Read(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Modify(ctx, "anthropic", func(*Credential) (*Credential, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Delete(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storage.received, []context.Context{ctx, ctx, ctx, ctx}) {
		t.Fatalf("received = %#v", storage.received)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/runtime-credentials.test.ts:70
func TestRuntimeCredentialsKeepsOverrideWhenDeletionCancelledUpstream(t *testing.T) {
	aborted := errors.New("cancelled")
	storage := &runtimeCredentialStoreProbe{InMemoryAuthStorage: NewInMemoryAuthStorage(map[string]Credential{"anthropic": {Type: CredentialAPIKey, Key: "stored-key"}}), deleteError: aborted}
	credentials := NewRuntimeCredentials(storage)
	credentials.SetRuntimeAPIKey("anthropic", "runtime-key")
	if err := credentials.Delete(t.Context(), "anthropic"); err != aborted {
		t.Fatalf("delete = %v want original cancellation", err)
	}
	if len(storage.received) != 1 {
		t.Fatalf("delete calls = %d", len(storage.received))
	}
	assertRuntimeCredential(t, credentials, "runtime-key")
}

// .upstream/v0.87.1/packages/coding-agent/test/runtime-credentials.test.ts:83
func TestRuntimeCredentialsDeleteClearsOverrideAndStoredUpstream(t *testing.T) {
	storage := NewInMemoryAuthStorage(map[string]Credential{"anthropic": {Type: CredentialAPIKey, Key: "stored-key"}})
	credentials := NewRuntimeCredentials(storage)
	credentials.SetRuntimeAPIKey("anthropic", "runtime-key")
	if err := credentials.Delete(t.Context(), "anthropic"); err != nil {
		t.Fatal(err)
	}
	got, err := credentials.Read(t.Context(), "anthropic")
	if got != nil || err != nil {
		t.Fatalf("read = %#v, %v", got, err)
	}
	infos, err := credentials.List(t.Context())
	if err != nil || !reflect.DeepEqual(infos, []CredentialInfo{}) {
		t.Fatalf("list = %#v, %v", infos, err)
	}
}

func assertRuntimeCredential(t *testing.T, store CredentialStore, key string) {
	t.Helper()
	got, err := store.Read(t.Context(), "anthropic")
	if err != nil || !reflect.DeepEqual(got, &Credential{Type: CredentialAPIKey, Key: key}) {
		t.Fatalf("credential = %#v, %v", got, err)
	}
}
