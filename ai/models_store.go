package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// ModelsStoreEntry mirrors pi-ai ModelsStoreEntry. Models stay raw JSON
// because each provider owns the shape of the catalog it persists.
type ModelsStoreEntry struct {
	Models []json.RawMessage `json:"models"`
	// LastModified is the Unix timestamp from the remote catalog's
	// Last-Modified header.
	LastModified *float64 `json:"lastModified,omitempty"`
	// CheckedAt is the Unix timestamp of the last completed remote check.
	CheckedAt *float64 `json:"checkedAt,omitempty"`
	// ETag is the remote catalog's opaque validator, stored verbatim.
	ETag string `json:"etag,omitempty"`
}

// Clone returns a deep copy, mirroring upstream's structuredClone at the
// store boundary.
func (entry ModelsStoreEntry) Clone() ModelsStoreEntry {
	models := make([]json.RawMessage, len(entry.Models))
	for index, model := range entry.Models {
		models[index] = bytes.Clone(model)
	}
	entry.Models = models
	if entry.LastModified != nil {
		entry.LastModified = new(*entry.LastModified)
	}
	if entry.CheckedAt != nil {
		entry.CheckedAt = new(*entry.CheckedAt)
	}
	return entry
}

// ModelsStore mirrors pi-ai ModelsStore: persistent model catalogs keyed by
// provider ID. A cancelled ctx plays the role of options.signal.
type ModelsStore interface {
	Read(ctx context.Context, providerID string) (*ModelsStoreEntry, error)
	Write(ctx context.Context, providerID string, entry ModelsStoreEntry) error
	Delete(ctx context.Context, providerID string) error
}

// InMemoryModelsStore mirrors pi-ai InMemoryModelsStore (and the coding
// agent's InMemoryCodingAgentModelsStore): a process-local ModelsStore.
type InMemoryModelsStore struct {
	mu      sync.Mutex
	entries map[string]ModelsStoreEntry
}

// NewInMemoryModelsStore returns an empty in-memory store.
func NewInMemoryModelsStore() *InMemoryModelsStore {
	return &InMemoryModelsStore{entries: map[string]ModelsStoreEntry{}}
}

// Read returns a copy of the provider's catalog, or nil when none is stored.
func (s *InMemoryModelsStore) Read(ctx context.Context, providerID string) (*ModelsStoreEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[providerID]
	if !ok {
		return nil, nil
	}
	cloned := entry.Clone()
	return &cloned, nil
}

// Write stores a copy of the provider's catalog.
func (s *InMemoryModelsStore) Write(ctx context.Context, providerID string, entry ModelsStoreEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[providerID] = entry.Clone()
	return nil
}

// Delete removes the provider's catalog.
func (s *InMemoryModelsStore) Delete(ctx context.Context, providerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, providerID)
	return nil
}

// FileModelsStore mirrors coding-agent core/models-store.ts FileModelsStore:
// locked JSON storage for dynamically refreshed provider catalogs, keyed by
// provider id, at <agentDir>/models-store.json by default.
type FileModelsStore struct {
	path      string
	readState *modelsFileReadState
	lock      func(context.Context, string, func(func() error) error) error
}

// NewFileModelsStore opens the store at path.
func NewFileModelsStore(path string) *FileModelsStore {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return &FileModelsStore{path: path, readState: modelsReadStateForPath(path), lock: withSidecarLock}
}

// Path returns the store file path.
func (s *FileModelsStore) Path() string { return s.path }

// Read returns a fresh copy of the provider's stored catalog. Concurrent readers share reloads and cancel their waits independently; unchanged files use the cached revision.
func (s *FileModelsStore) Read(ctx context.Context, providerID string) (*ModelsStoreEntry, error) {
	entries, err := s.readLatest(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, stored := range entries {
		if stored.providerID == providerID {
			var entry ModelsStoreEntry
			if err := json.Unmarshal(stored.raw, &entry); err != nil {
				return nil, fmt.Errorf("models store: parse %s: %w", providerID, err)
			}
			return &entry, nil
		}
	}
	return nil, nil
}

// Write replaces the provider's stored catalog.
func (s *FileModelsStore) Write(ctx context.Context, providerID string, entry ModelsStoreEntry) error {
	raw, err := marshalStoreJSON(entry)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func(entries []storedModels) ([]storedModels, error) {
		for index := range entries {
			if entries[index].providerID == providerID {
				entries[index].raw = raw
				return entries, nil
			}
		}
		return append(entries, storedModels{providerID: providerID, raw: raw}), nil
	})
}

// Delete removes the provider's stored catalog. Like upstream it rewrites
// the file even when the provider has no entry.
func (s *FileModelsStore) Delete(ctx context.Context, providerID string) error {
	return s.withLock(ctx, func(entries []storedModels) ([]storedModels, error) {
		return slices.DeleteFunc(append([]storedModels{}, entries...), func(stored storedModels) bool {
			return stored.providerID == providerID
		}), nil
	})
}

// storedModels is one provider entry; a slice keeps the file's key order the
// way the upstream JSON object does.
type storedModels struct {
	providerID string
	raw        json.RawMessage
}

// withLock mirrors FileAuthStorageBackend.withLockAsync: it creates the
// directory and the file as "{}" when existsSync finds neither, before it
// waits for the lock (auth-storage.ts:55-68,158-163), holds the lock across
// read-modify-write, writes only when fn returns entries, and returns Node's
// fs errors.
func (s *FileModelsStore) withLock(ctx context.Context, fn func([]storedModels) ([]storedModels, error)) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); !pathExists(dir) {
		if err := nodeerrno.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if !pathExists(s.path) {
		file, err := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, writeErr := file.WriteString("{}")
			closeErr := file.Close()
			if err := errors.Join(writeErr, closeErr); err != nil {
				return nodeerrno.FromPathError(err)
			}
		} else if !errors.Is(err, fs.ErrExist) {
			return nodeerrno.FromPathError(err)
		}
	}
	return s.lock(ctx, s.path, func(check func() error) error {
		data, err := os.ReadFile(s.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nodeerrno.FromPathError(err)
		}
		entries, err := parseStoredModels(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
		if err != nil {
			return err
		}
		next, err := fn(entries)
		if err != nil {
			return err
		}
		// withLockAsync's checkpoints: after fn, the compromise and then the abort; after the write, the compromise alone (auth-storage.ts:183-189).
		if err := check(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if next == nil {
			return nil
		}
		if err := writeStoredModels(s.path, next); err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		s.readState.mu.Lock()
		s.readState.data = next
		s.readState.revision = ""
		s.readState.mu.Unlock()
		return nil
	})
}

func parseStoredModels(data []byte) ([]storedModels, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("models store: parse: invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("models store: parse: expected a JSON object")
	}
	var entries []storedModels
	positions := make(map[string]int)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("models store: parse: %w", err)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("models store: parse: %w", err)
		}
		providerID := key.(string)
		if index, exists := positions[providerID]; exists {
			entries[index].raw = raw
		} else {
			positions[providerID] = len(entries)
			entries = append(entries, storedModels{providerID: providerID, raw: raw})
		}
	}
	return entries, nil
}

// writeStoredModels mirrors JSON.stringify(current, null, 2).
func writeStoredModels(path string, entries []storedModels) error {
	var object bytes.Buffer
	object.WriteByte('{')
	for index, entry := range entries {
		if index > 0 {
			object.WriteByte(',')
		}
		key, err := marshalStoreJSON(entry.providerID)
		if err != nil {
			return err
		}
		object.Write(key)
		object.WriteByte(':')
		object.Write(entry.raw)
	}
	object.WriteByte('}')
	var indented bytes.Buffer
	if err := json.Indent(&indented, object.Bytes(), "", "  "); err != nil {
		return fmt.Errorf("models store: encode: %w", err)
	}
	if err := os.WriteFile(path, indented.Bytes(), 0o600); err != nil {
		return nodeerrno.FromPathError(err)
	}
	return nil
}

func marshalStoreJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("models store: encode: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// withSidecarLock holds the shared Pi directory lease, checks it once acquired as withLockAsync does (compromise, then abort), and hands fn the lease's compromise check, withLockAsync's throwIfCompromised. Like FileAuthStorageBackend.withLockAsync (auth-storage.ts:191-198), it ignores a failure to release the lease.
func withSidecarLock(ctx context.Context, path string, fn func(func() error) error) (err error) {
	lock, err := pilock.Acquire(ctx, path)
	if err != nil {
		// FileModelsStore locks through FileAuthStorageBackend, which rethrows proper-lockfile's error unchanged (auth-storage.ts:141).
		return err
	}
	defer func() { _ = lock.Release() }()
	if err := lock.Check(); err != nil {
		return err
	}
	return fn(lock.Compromised)
}
