package sessiontesting

import (
	"context"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// InstrumentedStorage is a test-only transparent Storage decorator that
// records commit admission before delegating.
type InstrumentedStorage struct {
	StorageDecorator
	mu             sync.Mutex
	commitAttempts [][]session.Write
}

// NewInstrumentedStorage wraps delegate.
func NewInstrumentedStorage(delegate session.Storage) *InstrumentedStorage {
	return &InstrumentedStorage{StorageDecorator: NewStorageDecorator(delegate)}
}

// GetCommitAttempts returns the recorded transactions in admission order.
func (storage *InstrumentedStorage) GetCommitAttempts() [][]session.Write {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return slices.Clone(storage.commitAttempts)
}

// ClearCommitAttempts forgets recorded transactions without touching the
// delegate.
func (storage *InstrumentedStorage) ClearCommitAttempts() {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.commitAttempts = nil
}

// Commit records the transaction, then delegates.
func (storage *InstrumentedStorage) Commit(ctx context.Context, writes []session.Write) (session.CommitResult, error) {
	storage.mu.Lock()
	storage.commitAttempts = append(storage.commitAttempts, writes)
	storage.mu.Unlock()
	return storage.Storage.Commit(ctx, writes)
}
