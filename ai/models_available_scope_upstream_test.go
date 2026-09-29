package ai

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type readRecordingStore struct {
	CredentialStore
	mu    sync.Mutex
	reads []string
	fail  bool
}

func (s *readRecordingStore) Read(ctx context.Context, providerID string) (*Credential, error) {
	s.mu.Lock()
	s.reads = append(s.reads, providerID)
	fail := s.fail
	s.mu.Unlock()
	if fail {
		return nil, errors.New("read failed for " + providerID)
	}
	return s.CredentialStore.Read(ctx, providerID)
}

// packages/coding-agent/test/model-runtime-auth-options.test.ts:44 "scopes provider availability reads and records refresh failures": a provider-scoped availability check reads only that provider's credential, and a store failure surfaces as "Credential store read failed for <provider>".
func TestModelsAvailabilityScopesCredentialReadsUpstream(t *testing.T) {
	store := &readRecordingStore{CredentialStore: NewInMemoryAuthStorage(nil)}
	models := CreateModels(CreateModelsOptions{Credentials: store})
	t.Cleanup(models.Close)
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "anthropic"}))
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "other"}))

	store.mu.Lock()
	store.reads = nil
	store.mu.Unlock()
	if _, err := models.GetAvailable(t.Context(), "anthropic"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	reads := append([]string(nil), store.reads...)
	store.fail = true
	store.mu.Unlock()
	if len(reads) == 0 {
		t.Fatal("no credential read observed")
	}
	for _, id := range reads {
		if id != "anthropic" {
			t.Fatalf("availability read %q, want only anthropic: %v", id, reads)
		}
	}

	_, err := models.GetAvailable(t.Context(), "anthropic")
	var modelsErr *ModelsError
	if !errors.As(err, &modelsErr) || modelsErr.Code != ModelsErrorAuth || !strings.Contains(err.Error(), "Credential store read failed for anthropic") {
		t.Fatalf("GetAvailable error = %v", err)
	}

	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	if _, err := models.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
}
