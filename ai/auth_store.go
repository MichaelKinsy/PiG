package ai

// Credential-store contract for request-auth resolution. Mirrors upstream
// packages/ai/src/auth/types.ts (CredentialStore, CredentialInfo) and the
// coding-agent implementations in packages/coding-agent/src/core/auth-storage.ts
// (AuthStorage, ReadOnlyAuthStorage, AuthStorage.inMemory).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/configvalue"
)

// CredentialInfo is non-secret credential metadata for account/status
// enumeration. Mirrors upstream CredentialInfo.
type CredentialInfo struct {
	ProviderID string
	Type       CredentialType
}

// CredentialStore is app-owned credential storage keyed by provider id.
// Modify is the only write path used by request-auth resolution, so every
// OAuth refresh is a serialized read-modify-write. Mirrors upstream
// CredentialStore; delete is served by AuthStorage.Delete for logout.
type CredentialStore interface {
	// Read returns the stored credential, possibly expired, or nil when none
	// is stored. API-key values are resolved as configuration values.
	Read(ctx context.Context, providerID string) (*Credential, error)
	// List returns stored credential metadata without resolving values.
	List(ctx context.Context) ([]CredentialInfo, error)
	// Modify runs fn with the current raw credential under the store's
	// lock. A nil result leaves the entry unchanged. It returns the
	// post-write credential.
	Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error)
}

// resolveStoredCredential mirrors upstream AuthStorage.read: an API-key
// credential's key is resolved as a configuration value against its env.
func resolveStoredCredential(credential Credential) *Credential {
	out := cloneCredential(credential)
	if out.Type == CredentialAPIKey && out.Key != "" {
		out.Key = configvalue.Resolve(out.Key, out.Env)
	}
	return &out
}

func cloneCredential(credential Credential) Credential {
	credential.Env = maps.Clone(credential.Env)
	return credential
}

func credentialInfos(credentials map[string]Credential) []CredentialInfo {
	infos := make([]CredentialInfo, 0, len(credentials))
	for _, providerID := range slices.Sorted(maps.Keys(credentials)) {
		infos = append(infos, CredentialInfo{ProviderID: providerID, Type: credentials[providerID].Type})
	}
	return infos
}

// Read implements CredentialStore.
func (a *AuthStorage) Read(ctx context.Context, providerID string) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credentials, err := a.Load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credential, ok := credentials[providerID]
	if !ok {
		return nil, nil
	}
	return resolveStoredCredential(credential), nil
}

// List implements CredentialStore without resolving configured key values.
func (a *AuthStorage) List(ctx context.Context) ([]CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credentials, err := a.Load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return credentialInfos(credentials), nil
}

// Modify implements CredentialStore under the auth.json file lock.
func (a *AuthStorage) Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var result *Credential
	err := a.withFileLock(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		credentials, err := a.loadLocked()
		if err != nil {
			return err
		}
		var current *Credential
		if credential, ok := credentials[providerID]; ok {
			current = new(cloneCredential(credential))
		}
		next, err := fn(current)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if next == nil {
			result = current
			return nil
		}
		credentials[providerID] = cloneCredential(*next)
		if err := a.writeLocked(credentials); err != nil {
			return err
		}
		result = new(cloneCredential(*next))
		return nil
	})
	return result, err
}

// InMemoryAuthStorage is a process-local CredentialStore. Mirrors upstream
// AuthStorage.inMemory; it is a separate type because AuthStorage is bound to
// its auth.json path.
type InMemoryAuthStorage struct {
	mu          sync.Mutex
	credentials map[string]Credential
}

// NewInMemoryAuthStorage returns a store seeded with a copy of data.
func NewInMemoryAuthStorage(data map[string]Credential) *InMemoryAuthStorage {
	credentials := make(map[string]Credential, len(data))
	for providerID, credential := range data {
		credentials[providerID] = cloneCredential(credential)
	}
	return &InMemoryAuthStorage{credentials: credentials}
}

// Read implements CredentialStore.
func (s *InMemoryAuthStorage) Read(ctx context.Context, providerID string) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	credential, ok := s.credentials[providerID]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return resolveStoredCredential(credential), nil
}

// List implements CredentialStore.
func (s *InMemoryAuthStorage) List(ctx context.Context) ([]CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return credentialInfos(s.credentials), nil
}

// Modify implements CredentialStore; calls are serialized.
func (s *InMemoryAuthStorage) Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var current *Credential
	if credential, ok := s.credentials[providerID]; ok {
		current = new(cloneCredential(credential))
	}
	next, err := fn(current)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if next == nil {
		return current, nil
	}
	s.credentials[providerID] = cloneCredential(*next)
	return new(cloneCredential(*next)), nil
}

// ReadOnlyAuthStorage reads auth.json without creating it, its directory, or
// its lock, and never executes configured key commands. Mirrors upstream
// ReadOnlyAuthStorage.
type ReadOnlyAuthStorage struct {
	path string

	mu          sync.Mutex
	credentials map[string]Credential
}

// NewReadOnlyAuthStorage returns a read-only view of the auth.json at path.
func NewReadOnlyAuthStorage(path string) *ReadOnlyAuthStorage {
	return &ReadOnlyAuthStorage{path: path}
}

func (s *ReadOnlyAuthStorage) load() (map[string]Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credentials != nil {
		return s.credentials, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.credentials = map[string]Credential{}
		return s.credentials, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to read auth.json: %w", err)
	}
	var parsed any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(data), "\uFEFF")), &parsed); err != nil {
		return nil, fmt.Errorf("Failed to read auth.json: %w", err)
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, errors.New("Invalid auth.json: expected an object")
	}
	credentials := make(map[string]Credential, len(object))
	for providerID, value := range object {
		credential, ok := readOnlyCredential(value)
		if !ok {
			return nil, fmt.Errorf("Invalid auth.json credential for provider %q", providerID)
		}
		credentials[providerID] = credential
	}
	s.credentials = credentials
	return credentials, nil
}

// readOnlyCredential applies upstream ReadOnlyAuthStorage's shape checks.
func readOnlyCredential(value any) (Credential, bool) {
	fields, ok := value.(map[string]any)
	if !ok {
		return Credential{}, false
	}
	switch fields["type"] {
	case string(CredentialAPIKey):
		credential := Credential{Type: CredentialAPIKey}
		if raw, present := fields["key"]; present {
			key, ok := raw.(string)
			if !ok {
				return Credential{}, false
			}
			credential.Key = key
		}
		if raw, present := fields["env"]; present {
			env, ok := raw.(map[string]any)
			if !ok {
				return Credential{}, false
			}
			credential.Env = make(map[string]string, len(env))
			for name, entry := range env {
				text, ok := entry.(string)
				if !ok {
					return Credential{}, false
				}
				credential.Env[name] = text
			}
		}
		return credential, true
	case string(CredentialOAuth):
		access, accessOK := fields["access"].(string)
		refresh, refreshOK := fields["refresh"].(string)
		expires, expiresOK := fields["expires"].(float64)
		if !accessOK || !refreshOK || !expiresOK || math.IsInf(expires, 0) || math.IsNaN(expires) {
			return Credential{}, false
		}
		credential := Credential{Type: CredentialOAuth, Access: access, Refresh: refresh, Expires: int64(expires)}
		credential.ProjectID, _ = fields["projectId"].(string)
		credential.EnterpriseDomain, _ = fields["enterpriseUrl"].(string)
		return credential, true
	}
	return Credential{}, false
}

// Read implements CredentialStore. Command-valued keys are returned
// unresolved, as upstream does, so a read-only check never runs them.
func (s *ReadOnlyAuthStorage) Read(ctx context.Context, providerID string) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credentials, err := s.load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credential, ok := credentials[providerID]
	if !ok {
		return nil, nil
	}
	if credential.Type != CredentialAPIKey || credential.Key == "" || configvalue.IsCommandConfigValue(credential.Key) {
		return new(cloneCredential(credential)), nil
	}
	return resolveStoredCredential(credential), nil
}

// List implements CredentialStore.
func (s *ReadOnlyAuthStorage) List(ctx context.Context) ([]CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credentials, err := s.load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return credentialInfos(credentials), nil
}

// Modify always fails: the store is read-only.
func (s *ReadOnlyAuthStorage) Modify(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
	return nil, errors.New("Read-only credential storage cannot modify auth.json")
}
