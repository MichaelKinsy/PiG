package ai

// Credential-store contract for request-auth resolution. Mirrors upstream
// packages/ai/src/auth/types.ts (CredentialStore, CredentialInfo) and the
// coding-agent implementations in packages/coding-agent/src/core/auth-storage.ts
// (AuthStorage, ReadOnlyAuthStorage, AuthStorage.inMemory).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// CredentialInfo is non-secret credential metadata for account/status
// enumeration. Mirrors upstream CredentialInfo.
type CredentialInfo struct {
	ProviderID string
	Type       CredentialType
}

// CredentialStore is app-owned credential storage keyed by provider id.
// Modify serializes OAuth refreshes as read-modify-write operations; Delete
// removes a provider's credential for logout. Both honor the operation context.
type CredentialStore interface {
	// Read returns the stored credential, possibly expired, or nil when none
	// is stored. API-key values are resolved as configuration values.
	Read(ctx context.Context, providerID string) (*Credential, error)
	// List returns stored credential metadata in ECMAScript object-key order without resolving values.
	List(ctx context.Context) ([]CredentialInfo, error)
	// Modify runs fn with the current raw credential under the store's
	// lock. A nil result leaves the entry unchanged. It returns the
	// post-write credential.
	Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error)
	// Delete removes the stored credential, doing nothing if it is absent.
	Delete(ctx context.Context, providerID string) error
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

// cloneCredential isolates all mutable provider metadata from callers and uncommitted mutations.
func cloneCredential(credential Credential) Credential {
	credential.Extra = cloneCredentialExtra(credential.Extra)
	credential.Env = maps.Clone(credential.Env)
	credential.AvailableModelIDs = slices.Clone(credential.AvailableModelIDs)
	credential.GatewayConfig = slices.Clone(credential.GatewayConfig)
	return credential
}

// authStorageData retains JSON object order through mutations, persistence, and credential enumeration. Integer-index keys precede other keys, as with Object.entries. The shared JSON codec keeps lone UTF-16 provider IDs distinct from replacement characters in both lookup and persistence.
type authStorageData struct {
	values map[string]Credential
	order  []string
}

func newAuthStorageData(values map[string]Credential) *authStorageData {
	data := &authStorageData{values: make(map[string]Credential, len(values))}
	// A Go map has no insertion order; sort only the unordered constructor input. Subsequent mutations retain their insertion order.
	for _, key := range slices.Sorted(maps.Keys(values)) {
		data.set(key, cloneCredential(values[key]))
	}
	return data
}

func (data *authStorageData) set(providerID string, credential Credential) {
	if _, exists := data.values[providerID]; !exists {
		data.order = javascriptObjectKeyOrder(append(data.order, providerID))
	}
	data.values[providerID] = credential
}

func (data *authStorageData) delete(providerID string) {
	delete(data.values, providerID)
	data.order = slices.DeleteFunc(data.order, func(key string) bool { return key == providerID })
}

func (data *authStorageData) infos() []CredentialInfo {
	infos := make([]CredentialInfo, 0, len(data.order))
	for _, providerID := range data.order {
		infos = append(infos, CredentialInfo{ProviderID: providerID, Type: data.values[providerID].Type})
	}
	return infos
}

func (data *authStorageData) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, providerID := range data.order {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(providerID)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(data.values[providerID])
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// authStorageObjectKeys reads the first insertion position of each key from a validated JSON object. JSON.parse replaces duplicate values without moving their keys.
func authStorageObjectKeys(raw []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var keys []string
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("auth object key is not a string")
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
	}
	return javascriptObjectKeyOrder(keys), nil
}

// Read implements CredentialStore. Concurrent readers share a reload; canceling one reader leaves the others running. A read without a cancellable context falls back to the last valid snapshot on reload failure.
func (a *AuthStorage) Read(ctx context.Context, providerID string) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credentials, err := a.readCredentialData(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credential, ok := credentials.values[providerID]
	if !ok {
		return nil, nil
	}
	return resolveStoredCredential(credential), nil
}

// List implements CredentialStore in ECMAScript object-key order without resolving configured key values. It shares Read's reload, cancellation, and unsignalled fallback behavior.
func (a *AuthStorage) List(ctx context.Context) ([]CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credentials, err := a.readCredentialData(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return credentials.infos(), nil
}

// Modify implements CredentialStore under the auth.json file lock. A caller waiting for process-local ownership can cancel without waiting for the active callback.
func (a *AuthStorage) Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if err := a.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer a.mu.Unlock()
	var result *Credential
	err := a.withFileLock(ctx, true, func(lock *pilock.Lock) error {
		// The lock's context carries a heartbeat compromise as its cause, which withLockAsync's throwIfCompromised throws before the caller's abort (auth-storage.ts:168-172,180-184).
		ctx := lock.Context()
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		credentials, err := a.loadLocked()
		if err != nil {
			return err
		}
		var current *Credential
		if credential, ok := credentials.values[providerID]; ok {
			current = new(cloneCredential(credential))
		}
		next, err := fn(current)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if next == nil {
			a.read.mu.Lock()
			a.read.creds = credentials
			a.read.revision, _ = authFileRevision(a.path)
			a.read.mu.Unlock()
			result = current
			return nil
		}
		credentials.set(providerID, cloneCredential(*next))
		if err := a.writeLocked(credentials, lock); err != nil {
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
	pending     []memoryAuthOperation
	credentials *authStorageData // Accessed only by the active queued operation.
}

type memoryAuthOperation struct {
	ctx    context.Context
	run    func() error
	result chan error
}

// withLockAsync preserves the whole-store Promise chain independently of each caller's cancellation. The worker owns canceled callbacks through settlement; queued callbacks check cancellation before running.
// upstream: packages/coding-agent/src/core/auth-storage.ts:InMemoryAuthStorageBackend
func (s *InMemoryAuthStorage) withLockAsync(ctx context.Context, run func() error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	result := make(chan error, 1)
	s.mu.Lock()
	s.pending = append(s.pending, memoryAuthOperation{ctx: ctx, run: run, result: result})
	if len(s.pending) == 1 {
		go s.runPending()
	}
	s.mu.Unlock()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *InMemoryAuthStorage) runPending() {
	for {
		s.mu.Lock()
		op := s.pending[0]
		s.mu.Unlock()
		err := context.Cause(op.ctx)
		if err == nil {
			err = op.run()
		}
		// The buffered result observes settlement even when cancellation has released the caller.
		op.result <- err
		s.mu.Lock()
		s.pending[0] = memoryAuthOperation{}
		s.pending = s.pending[1:]
		if len(s.pending) == 0 {
			s.pending = nil
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// NewInMemoryAuthStorage returns a store seeded with a copy of data. Seed keys use lexical order because Go maps have no insertion order; later writes retain their insertion order.
func NewInMemoryAuthStorage(data map[string]Credential) *InMemoryAuthStorage {
	return &InMemoryAuthStorage{credentials: newAuthStorageData(data)}
}

// Read returns an isolated credential snapshot after preceding store operations settle. Cancellation releases the caller without bypassing an active mutation.
func (s *InMemoryAuthStorage) Read(ctx context.Context, providerID string) (*Credential, error) {
	var credential *Credential
	if err := s.withLockAsync(ctx, func() error {
		if current, ok := s.credentials.values[providerID]; ok {
			credential = new(cloneCredential(current))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, nil
	}
	return resolveStoredCredential(*credential), nil
}

// List implements CredentialStore after preceding store operations settle.
func (s *InMemoryAuthStorage) List(ctx context.Context) ([]CredentialInfo, error) {
	var infos []CredentialInfo
	if err := s.withLockAsync(ctx, func() error {
		infos = s.credentials.infos()
		return nil
	}); err != nil {
		return nil, err
	}
	return infos, nil
}

// Modify serializes callbacks across providers with isolated input and result snapshots. Cancellation releases the caller immediately, but an active callback keeps its queue position until settlement and cannot commit a canceled write.
func (s *InMemoryAuthStorage) Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error) {
	var result *Credential
	if err := s.withLockAsync(ctx, func() error {
		var current *Credential
		if credential, ok := s.credentials.values[providerID]; ok {
			current = new(cloneCredential(credential))
		}
		next, err := fn(current)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if next == nil {
			result = current
			return nil
		}
		s.credentials.set(providerID, cloneCredential(*next))
		result = new(cloneCredential(*next))
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// Delete removes a credential in the same cancellable queue as Modify.
func (s *InMemoryAuthStorage) Delete(ctx context.Context, providerID string) error {
	return s.withLockAsync(ctx, func() error {
		s.credentials.delete(providerID)
		return nil
	})
}

// ReadOnlyAuthStorage reads auth.json without creating it, its directory, or
// its lock, and never executes configured key commands. Mirrors upstream
// ReadOnlyAuthStorage.
type ReadOnlyAuthStorage struct {
	path string

	mu          sync.Mutex
	credentials *authStorageData
}

// NewReadOnlyAuthStorage returns a read-only view of the auth.json at path.
func NewReadOnlyAuthStorage(path string) *ReadOnlyAuthStorage {
	return &ReadOnlyAuthStorage{path: path}
}

func (s *ReadOnlyAuthStorage) load() (*authStorageData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credentials != nil {
		return s.credentials, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.credentials = newAuthStorageData(nil)
		return s.credentials, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to read auth.json: %w", err)
	}
	// Use the same lossless key decoder as writable storage before validating each credential.
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			return nil, errors.New("Invalid auth.json: expected an object")
		}
		return nil, fmt.Errorf("Failed to read auth.json: %w", err)
	}
	if object == nil {
		return nil, errors.New("Invalid auth.json: expected an object")
	}
	order, err := authStorageObjectKeys(data)
	if err != nil {
		return nil, fmt.Errorf("Failed to read auth.json: %w", err)
	}
	credentials := &authStorageData{values: make(map[string]Credential, len(object)), order: order}
	for _, providerID := range order {
		credential, ok := readOnlyCredential(object[providerID])
		if !ok {
			return nil, fmt.Errorf("Invalid auth.json credential for provider %q", providerID)
		}
		credentials.values[providerID] = credential
	}
	s.credentials = credentials
	return credentials, nil
}

// readOnlyCredential validates the required fields without projecting away provider-owned metadata.
func readOnlyCredential(raw json.RawMessage) (Credential, bool) {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
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
		return retainReadOnlyCredentialFields(raw, credential)
	case string(CredentialOAuth):
		access, accessOK := fields["access"].(string)
		refresh, refreshOK := fields["refresh"].(string)
		expires, expiresOK := fields["expires"].(float64)
		if !accessOK || !refreshOK || !expiresOK || math.IsInf(expires, 0) || math.IsNaN(expires) {
			return Credential{}, false
		}
		credential := Credential{Type: CredentialOAuth, Access: access, Refresh: refresh}
		credential.SetExpiresMillis(expires)
		credential.ProjectID, _ = fields["projectId"].(string)
		credential.AccountID, _ = fields["accountId"].(string)
		credential.EnterpriseDomain, _ = fields["enterpriseUrl"].(string)
		credential.Scope, _ = fields["scope"].(string)
		return retainReadOnlyCredentialFields(raw, credential)
	}
	return Credential{}, false
}

// retainReadOnlyCredentialFields preserves unvalidated metadata and explicit empty properties as raw JSON. Known fields come only from their exact names, so an opaque differently-cased key cannot overwrite a validated field.
func retainReadOnlyCredentialFields(raw json.RawMessage, credential Credential) (Credential, bool) {
	known, err := json.Marshal(credential)
	if err != nil {
		return Credential{}, false
	}
	credential.Extra, err = credentialExtra(raw, known, credential.Type == CredentialOAuth)
	if err != nil {
		return Credential{}, false
	}
	if value, ok := credential.Extra["gatewayConfig"]; ok {
		credential.GatewayConfig = bytes.Clone(value)
		delete(credential.Extra, "gatewayConfig")
	}
	if value, ok := credential.Extra["availableModelIds"]; ok {
		credential.AvailableModelIDs = bytes.Clone(value)
		delete(credential.Extra, "availableModelIds")
	}
	if credential.Env == nil {
		var values map[string]any
		if value, ok := credential.Extra["env"]; ok && json.Unmarshal(value, &values) == nil && len(values) > 0 {
			env := make(map[string]string, len(values))
			valid := true
			for key, value := range values {
				text, ok := value.(string)
				if !ok {
					valid = false
					break
				}
				env[key] = text
			}
			if valid {
				credential.Env = env
				delete(credential.Extra, "env")
			}
		}
	}
	if len(credential.Extra) == 0 {
		credential.Extra = nil
	}
	return credential, true
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
	credential, ok := credentials.values[providerID]
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
	return credentials.infos(), nil
}

// Modify always fails: the store is read-only.
func (s *ReadOnlyAuthStorage) Modify(context.Context, string, func(*Credential) (*Credential, error)) (*Credential, error) {
	return nil, errors.New("Read-only credential storage cannot modify auth.json")
}

// Delete always fails: the store is read-only.
func (s *ReadOnlyAuthStorage) Delete(context.Context, string) error {
	return errors.New("Read-only credential storage cannot modify auth.json")
}

// ReadRawCredential uses GetRaw when the store exposes it. Otherwise it calls Read, which may resolve configuration values according to the supplied store's contract.
func ReadRawCredential(ctx context.Context, store CredentialStore, providerID string) (Credential, bool, error) {
	if raw, ok := store.(interface {
		GetRaw(string) (Credential, bool, error)
	}); ok {
		return raw.GetRaw(providerID)
	}
	credential, err := store.Read(ctx, providerID)
	if err != nil || credential == nil {
		return Credential{}, false, err
	}
	return *credential, true, nil
}

// CredentialStoreProviderEnv returns the provider-scoped environment overrides of providerID's API-key credential in store, or nil. It mirrors AuthStorage.GetProviderEnv over any CredentialStore.
func CredentialStoreProviderEnv(ctx context.Context, store CredentialStore, providerID string) (map[string]string, error) {
	credential, ok, err := ReadRawCredential(ctx, store, providerID)
	if err != nil || !ok || credential.Type != CredentialAPIKey || len(credential.Env) == 0 {
		return nil, err
	}
	return credential.Env, nil
}

// CredentialStoreAuthStatus reports whether auth is configured for providerID in store without refreshing OAuth tokens. It mirrors AuthStorage.GetAuthStatus over any CredentialStore.
func CredentialStoreAuthStatus(ctx context.Context, store CredentialStore, providerID string) AuthStatus {
	if _, ok, err := ReadRawCredential(ctx, store, providerID); err == nil && ok {
		return AuthStatus{Configured: true, Source: AuthSourceStored}
	}
	if key := firstAuthEnvKey(providerID); key != "" {
		return AuthStatus{Configured: false, Source: AuthSourceEnvironment, Label: key}
	}
	return AuthStatus{Configured: false}
}
