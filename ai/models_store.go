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
	"time"
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
	path string
	mu   sync.Mutex
}

// NewFileModelsStore opens the store at path.
func NewFileModelsStore(path string) *FileModelsStore {
	return &FileModelsStore{path: path}
}

// Path returns the store file path.
func (s *FileModelsStore) Path() string { return s.path }

// Read returns the provider's stored catalog, or nil when none is stored.
func (s *FileModelsStore) Read(ctx context.Context, providerID string) (*ModelsStoreEntry, error) {
	var entry *ModelsStoreEntry
	err := s.withLock(ctx, func(entries []storedModels) ([]storedModels, error) {
		for _, stored := range entries {
			if stored.providerID != providerID {
				continue
			}
			var decoded ModelsStoreEntry
			if err := json.Unmarshal(stored.raw, &decoded); err != nil {
				return nil, fmt.Errorf("models store: parse %s: %w", providerID, err)
			}
			entry = &decoded
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return entry, ctx.Err()
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

// withLock mirrors FileAuthStorageBackend.withLockAsync: it creates the file
// as "{}" when missing, holds the lock across read-modify-write, and writes
// only when fn returns entries.
func (s *FileModelsStore) withLock(ctx context.Context, fn func([]storedModels) ([]storedModels, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("models store: ensure dir: %w", err)
	}
	if _, err := os.Stat(s.path); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(s.path, []byte("{}"), 0o600); err != nil {
			return fmt.Errorf("models store: create: %w", err)
		}
	}
	return withSidecarLock(s.path, func() error {
		data, err := os.ReadFile(s.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("models store: read: %w", err)
		}
		entries, err := parseStoredModels(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
		if err != nil {
			return err
		}
		next, err := fn(entries)
		if err != nil || next == nil {
			return err
		}
		return writeStoredModels(s.path, next)
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
	return os.WriteFile(path, indented.Bytes(), 0o600)
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

// withSidecarLock holds the same kind of sidecar flock AuthStorage uses.
func withSidecarLock(path string, fn func() error) error {
	lockPath := path + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("models store: open lock: %w", err)
		}
		locked, err := tryLockFile(file)
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("models store: lock: %w", err)
		}
		if locked {
			defer func() {
				unlockFile(file)
				_ = file.Close()
			}()
			return fn()
		}
		_ = file.Close()
		if time.Now().After(deadline) {
			return errors.New("models store: timed out acquiring lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
