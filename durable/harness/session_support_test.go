// Ports the parts of packages/durable/test/session-support.ts that the harness tests use.

package harness

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// gate holds calls until released and reports when the first held call arrives.
type gate struct {
	entered *deferredGate
	held    *deferredGate
}

func (g gate) release() { g.held.resolve() }

// controlledStorage is memory storage with observable commits, held calls, and injected commit failures.
type controlledStorage struct {
	*storage.MemoryStorage
	mu            sync.Mutex
	commits       [][]durable.StorageWrite
	commitGate    *gate
	findGate      *gate
	commitFailure error
	closeFailure  error
}

func newControlledStorage() *controlledStorage {
	return &controlledStorage{MemoryStorage: storage.NewMemoryStorage()}
}

func (s *controlledStorage) holdCommits() gate {
	g := gate{entered: deferred(), held: deferred()}
	s.mu.Lock()
	s.commitGate = &g
	s.mu.Unlock()
	return g
}

func (s *controlledStorage) holdFindDocument() gate {
	g := gate{entered: deferred(), held: deferred()}
	s.mu.Lock()
	s.findGate = &g
	s.mu.Unlock()
	return g
}

func (s *controlledStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	s.mu.Lock()
	held := s.findGate
	s.mu.Unlock()
	if held != nil {
		held.entered.resolve()
		_ = held.held.wait(context.Background())
		s.mu.Lock()
		if s.findGate == held {
			s.findGate = nil
		}
		s.mu.Unlock()
	}
	return s.MemoryStorage.FindDocument(ctx, address, at)
}

func (s *controlledStorage) failNextCommit(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitFailure = err
}

func (s *controlledStorage) commitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.commits)
}

func (s *controlledStorage) commitAt(index int) []durable.StorageWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits[index]
}

func (s *controlledStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	s.commits = append(s.commits, append([]durable.StorageWrite(nil), writes...))
	held := s.commitGate
	s.mu.Unlock()
	if held != nil {
		held.entered.resolve()
		_ = held.held.wait(context.Background())
		s.mu.Lock()
		if s.commitGate == held {
			s.commitGate = nil
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	failure := s.commitFailure
	s.commitFailure = nil
	s.mu.Unlock()
	if failure != nil {
		return 0, failure
	}
	return s.MemoryStorage.Commit(ctx, writes)
}

func (s *controlledStorage) Close(ctx context.Context) error {
	if err := s.MemoryStorage.Close(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeFailure
}

// documentChanges returns the document changes of a publication.
func documentChanges(publication durable.CommitPublication) []durable.DocumentChange {
	var changes []durable.DocumentChange
	for _, change := range publication.Changes {
		if document, ok := change.(durable.DocumentChange); ok {
			changes = append(changes, document)
		}
	}
	return changes
}
