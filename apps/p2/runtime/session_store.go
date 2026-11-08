package runtime

import (
	"context"

	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/src/runtime/session-store.ts: a session's Durable records in its Chord store are `DurableStorage` (worker.go)
// under `DurableOwner`; the worker opens it over the store its host runs on, and offline tools (the migration, tests,
// reports) open a stopped session's store with OpenSessionStorage.

// SessionStorage is a stopped session's store and its Durable storage.
type SessionStorage struct {
	Store   chordstore.Store
	Storage durable.Storage
}

// OpenSessionStorage opens the store of the stopped session id (no worker may run it: one process per store) and its
// Durable storage; Close closes both.
func OpenSessionStorage(ctx context.Context, dataDir, id string) (*SessionStorage, error) {
	dir, err := SessionDirectory(dataDir, id)
	if err != nil {
		return nil, err
	}
	store, err := OpenHostStore(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	storage, err := DurableStorage(ctx, store)
	if err != nil {
		_ = store.Close(ctx)
		return nil, err
	}
	return &SessionStorage{Store: store, Storage: storage}, nil
}

// Close closes the storage, then the store.
func (s *SessionStorage) Close(ctx context.Context) error {
	if err := s.Storage.Close(ctx); err != nil {
		return err
	}
	return s.Store.Close(ctx)
}
