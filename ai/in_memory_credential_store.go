package ai

// Ports packages/ai/src/auth/credential-store.ts.

import (
	"context"
	"slices"
	"sync"
)

// InMemoryCredentialStore stores literal credentials and serializes mutations per provider. Unlike app AuthStorage, it does not evaluate configuration expressions.
type InMemoryCredentialStore struct {
	mu          sync.Mutex
	credentials map[string]Credential
	order       []string
	chains      map[string]chan struct{}
	operations  sync.WaitGroup
}

func NewInMemoryCredentialStore() *InMemoryCredentialStore {
	return &InMemoryCredentialStore{credentials: map[string]Credential{}, chains: map[string]chan struct{}{}}
}

func (s *InMemoryCredentialStore) Read(ctx context.Context, providerID string) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.credentials[providerID]
	if !ok {
		return nil, nil
	}
	return new(cloneCredential(credential)), nil
}

func (s *InMemoryCredentialStore) List(ctx context.Context) ([]CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CredentialInfo, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, CredentialInfo{ProviderID: id, Type: s.credentials[id].Type})
	}
	return out, nil
}

type credentialMutationResult struct {
	credential *Credential
	err        error
}

func (s *InMemoryCredentialStore) enqueue(ctx context.Context, providerID string, task func() (*Credential, error)) (*Credential, error) {
	s.mu.Lock()
	previous := s.chains[providerID]
	done := make(chan struct{})
	s.chains[providerID] = done
	s.mu.Unlock()
	result := make(chan credentialMutationResult, 1)
	s.operations.Go(func() {
		defer func() {
			s.mu.Lock()
			if s.chains[providerID] == done {
				delete(s.chains, providerID)
			}
			close(done)
			s.mu.Unlock()
		}()
		if previous != nil {
			<-previous
		}
		if ctx.Err() != nil {
			result <- credentialMutationResult{err: context.Cause(ctx)}
			return
		}
		credential, err := task()
		result <- credentialMutationResult{credential, err}
	})
	select {
	case next := <-result:
		return next.credential, next.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (s *InMemoryCredentialStore) Modify(ctx context.Context, providerID string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
	return s.enqueue(ctx, providerID, func() (*Credential, error) {
		current, err := s.Read(ctx, providerID)
		if err != nil {
			return nil, err
		}
		next, err := fn(current)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		if next == nil {
			return current, nil
		}
		s.mu.Lock()
		if _, exists := s.credentials[providerID]; !exists {
			s.order = append(s.order, providerID)
		}
		s.credentials[providerID] = cloneCredential(*next)
		s.mu.Unlock()
		return new(cloneCredential(*next)), nil
	})
}

func (s *InMemoryCredentialStore) Delete(ctx context.Context, providerID string) error {
	_, err := s.enqueue(ctx, providerID, func() (*Credential, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.credentials, providerID)
		s.order = slices.DeleteFunc(s.order, func(id string) bool { return id == providerID })
		return nil, nil
	})
	return err
}
