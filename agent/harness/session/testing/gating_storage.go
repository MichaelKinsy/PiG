package sessiontesting

import (
	"context"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// CommitDiscarded rejects every commit after simulated storage loss.
type CommitDiscarded struct{ Message string }

func (err *CommitDiscarded) Error() string { return err.Message }

type parkedCommit struct {
	release chan struct{}
	dropErr error
	landing chan struct{}
	landErr error
}

// GatingStorage is a test-only decorator that deterministically parks
// admitted commits once armed.
type GatingStorage struct {
	StorageDecorator
	mu        sync.Mutex
	changed   *sync.Cond
	armed     bool
	discarded bool
	queue     []*parkedCommit
}

// NewGatingStorage wraps delegate; setup commits bypass gating until Arm.
func NewGatingStorage(delegate session.Storage) *GatingStorage {
	storage := &GatingStorage{StorageDecorator: NewStorageDecorator(delegate)}
	storage.changed = sync.NewCond(&storage.mu)
	return storage
}

// Arm starts parking commits.
func (storage *GatingStorage) Arm() {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.armed = true
}

// Pending reports the number of parked commits.
func (storage *GatingStorage) Pending() int {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return len(storage.queue)
}

var errPendingCount = errors.New("Pending commit count must be a positive safe integer")

// WaitPending blocks until at least count commits are parked.
func (storage *GatingStorage) WaitPending(count int) error {
	if count < 1 {
		return errPendingCount
	}
	storage.mu.Lock()
	defer storage.mu.Unlock()
	for !storage.discarded && len(storage.queue) < count {
		storage.changed.Wait()
	}
	if storage.discarded {
		return &CommitDiscarded{Message: "storage discarded"}
	}
	return nil
}

// Commit parks an armed commit until Next releases it, then delegates.
func (storage *GatingStorage) Commit(ctx context.Context, writes []session.Write) (session.CommitResult, error) {
	storage.mu.Lock()
	if storage.discarded {
		storage.mu.Unlock()
		return session.CommitResult{}, &CommitDiscarded{Message: "commit rejected: storage discarded"}
	}
	if !storage.armed {
		storage.mu.Unlock()
		return storage.Storage.Commit(ctx, writes)
	}
	parked := &parkedCommit{release: make(chan struct{}), landing: make(chan struct{})}
	storage.queue = append(storage.queue, parked)
	storage.changed.Broadcast()
	storage.mu.Unlock()

	<-parked.release
	result, err := storage.releasedCommit(ctx, parked, writes)
	parked.landErr = err
	close(parked.landing)
	return result, err
}

func (storage *GatingStorage) releasedCommit(ctx context.Context, parked *parkedCommit, writes []session.Write) (session.CommitResult, error) {
	if parked.dropErr != nil {
		return session.CommitResult{}, parked.dropErr
	}
	storage.mu.Lock()
	discarded := storage.discarded
	storage.mu.Unlock()
	if discarded {
		return session.CommitResult{}, &CommitDiscarded{Message: "commit rejected: storage discarded"}
	}
	return storage.Storage.Commit(ctx, writes)
}

var errReleasedCount = errors.New("Released commit count must be a positive safe integer")

// Next releases count commits in FIFO order, returning after each lands.
func (storage *GatingStorage) Next(count int) error {
	if count < 1 {
		return errReleasedCount
	}
	for range count {
		if err := storage.WaitPending(1); err != nil {
			return err
		}
		storage.mu.Lock()
		parked := storage.queue[0]
		storage.queue = storage.queue[1:]
		storage.mu.Unlock()
		close(parked.release)
		<-parked.landing
		if parked.landErr != nil {
			return parked.landErr
		}
	}
	return nil
}

// Discard drops parked commits and permanently rejects every later commit.
func (storage *GatingStorage) Discard() {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.discarded {
		return
	}
	storage.discarded = true
	for _, parked := range storage.queue {
		parked.dropErr = &CommitDiscarded{Message: "commit discarded"}
		close(parked.release)
	}
	storage.queue = nil
	storage.changed.Broadcast()
}
