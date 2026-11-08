package models

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/models/model-store.ts (adapted upstream from Pi's models-store.ts, MIT): a small locked catalog
// store. Provider code owns catalog refresh and publication.

var errInvalidCatalogs = errors.New("Invalid cached model catalogs")

// parseCatalogsValue validates the stored catalogs: an object of entries with a `models` array of objects whose id,
// name, api, provider and baseUrl are strings.
func parseCatalogsValue(value any) (*chordjson.Object, error) {
	object, ok := value.(*chordjson.Object)
	if !ok {
		return nil, errInvalidCatalogs
	}
	for _, entry := range object.All() {
		catalog, isObject := entry.(*chordjson.Object)
		if !isObject {
			return nil, errInvalidCatalogs
		}
		models, ok := catalog.Value("models").([]any)
		if !ok {
			return nil, errInvalidCatalogs
		}
		for _, model := range models {
			record, ok := model.(*chordjson.Object)
			if !ok {
				return nil, errInvalidCatalogs
			}
			for _, key := range []string{"id", "name", "api", "provider", "baseUrl"} {
				if _, isString := record.Value(key).(string); !isString {
					return nil, errInvalidCatalogs
				}
			}
		}
	}
	return object, nil
}

func parseCatalogs(content *string) (*chordjson.Object, error) {
	text := "{}"
	if content != nil {
		text = *content
	}
	value, err := chordjson.Decode([]byte(p2runtime.StripBOM(text)))
	if err != nil {
		return nil, errInvalidCatalogs
	}
	return parseCatalogsValue(value)
}

// FileModelsStore is the catalog store of models-store.json: reads cache the parsed file until its fingerprint changes.
type FileModelsStore struct {
	file *p2runtime.LockedFile
	path string

	mu       sync.Mutex
	cached   *chordjson.Object
	revision string
	loaded   bool
}

var _ ai.ModelsStore = (*FileModelsStore)(nil)

// NewFileModelsStore is the store of the file at path.
func NewFileModelsStore(path string) *FileModelsStore {
	normalized := p2runtime.NormalizePath(path)
	return &FileModelsStore{file: p2runtime.NewLockedFile(normalized), path: normalized, cached: chordjson.NewObject(0)}
}

// Fingerprint is the file's `ino:mtimeNs:ctimeNs:size`, "missing" when it does not exist.
func (store *FileModelsStore) Fingerprint() (string, error) {
	fingerprint, err := fileFingerprint(store.path)
	if err != nil {
		return "", errors.New("Could not read cached model catalogs")
	}
	return fingerprint, nil
}

// Read is the cached catalog of providerID, nil when there is none.
func (store *FileModelsStore) Read(ctx context.Context, providerID string) (*ai.ModelsStoreEntry, error) {
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	store.mu.Lock()
	current, err := store.Fingerprint()
	if err != nil {
		store.mu.Unlock()
		return nil, err
	}
	stale := !store.loaded || store.revision != current
	store.mu.Unlock()
	if stale {
		type snapshot struct {
			data     *chordjson.Object
			revision string
		}
		loaded, err := p2runtime.WithLock(ctx, store.file, func(content *string) (p2runtime.LockResult[snapshot], error) {
			data, err := parseCatalogs(content)
			if err != nil {
				return p2runtime.LockResult[snapshot]{}, err
			}
			revision, err := store.Fingerprint()
			if err != nil {
				return p2runtime.LockResult[snapshot]{}, err
			}
			return p2runtime.LockResult[snapshot]{Result: snapshot{data, revision}}, nil
		})
		if err != nil {
			return nil, err
		}
		store.mu.Lock()
		store.cached, store.revision, store.loaded = loaded.data, loaded.revision, true
		store.mu.Unlock()
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	store.mu.Lock()
	entry, ok := store.cached.Get(providerID)
	store.mu.Unlock()
	if !ok {
		return nil, nil
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var decoded ai.ModelsStoreEntry
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func (store *FileModelsStore) update(ctx context.Context, change func(catalogs *chordjson.Object) error) error {
	data, err := p2runtime.WithLock(ctx, store.file, func(content *string) (p2runtime.LockResult[*chordjson.Object], error) {
		catalogs, err := parseCatalogs(content)
		if err != nil {
			return p2runtime.LockResult[*chordjson.Object]{}, err
		}
		next := catalogs.Clone()
		if err := change(next); err != nil {
			return p2runtime.LockResult[*chordjson.Object]{}, err
		}
		if _, err := parseCatalogsValue(next); err != nil {
			return p2runtime.LockResult[*chordjson.Object]{}, err
		}
		encoded, err := chordjson.Marshal(next)
		if err != nil {
			return p2runtime.LockResult[*chordjson.Object]{}, err
		}
		text := string(encoded)
		return p2runtime.LockResult[*chordjson.Object]{Result: next, Next: &text}, nil
	})
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.cached, store.loaded = data, false
	store.mu.Unlock()
	return nil
}

// Write stores the catalog of providerID.
func (store *FileModelsStore) Write(ctx context.Context, providerID string, entry ai.ModelsStoreEntry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	value, err := chordjson.Decode(encoded)
	if err != nil {
		return err
	}
	return store.update(ctx, func(catalogs *chordjson.Object) error {
		catalogs.Set(providerID, value)
		return nil
	})
}

// Delete removes the catalog of providerID.
func (store *FileModelsStore) Delete(ctx context.Context, providerID string) error {
	return store.update(ctx, func(catalogs *chordjson.Object) error {
		catalogs.Delete(providerID)
		return nil
	})
}

// Close refuses later operations and waits for the running ones.
func (store *FileModelsStore) Close() { store.file.Close() }
