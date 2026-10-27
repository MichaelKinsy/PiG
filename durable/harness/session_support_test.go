// Ports the parts of packages/durable/test/session-support.ts that the harness tests use.

package harness

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
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
	// adopting is set while adopt runs, the storage's synchronous step: code that sees it set runs inside Storage.Commit's
	// adopt (session-support.ts ControlledStorage.adopting).
	adopting atomic.Bool
	// afterAdopt runs once, in the next commit's synchronous step right after adopt returned.
	afterAdopt func()
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
		_ = held.held.wait(crashedProcesses)
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

func (s *controlledStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	s.mu.Lock()
	s.commits = append(s.commits, append([]durable.StorageWrite(nil), writes...))
	held := s.commitGate
	s.mu.Unlock()
	if held != nil {
		held.entered.resolve()
		_ = held.held.wait(crashedProcesses)
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
	return s.MemoryStorage.Commit(ctx, writes, func(seq durable.Seq) {
		s.adopting.Store(true)
		func() {
			defer s.adopting.Store(false)
			adopt(seq)
		}()
		s.mu.Lock()
		after := s.afterAdopt
		s.afterAdopt = nil
		s.mu.Unlock()
		if after != nil {
			after()
		}
	})
}

// setAfterAdopt sets what the next commit runs right after adopt returned.
func (s *controlledStorage) setAfterAdopt(after func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterAdopt = after
}

var errReclaimFailed = errors.New("reclaim failed")

// lateWork is how long lateResolvingStorage works after adopting: a macrotask upstream, long enough here for the Session to
// notice a commit parked in work after adopt on an unloaded machine. It is not a guarantee: the Session publishes once
// the storage's goroutine is parked in a wait, which under heavy load can take longer, so a gap that needs what adopt
// published (host listeners, the waits it resolved) waits for that effect itself.
const lateWork = 25 * time.Millisecond

// lateResolvingStorage is controlled storage whose commits resolve a scheduling turn after they adopted the batch, as a
// storage with work after durability does (JSONL reclaims sidecars): code woken inside adopt runs before the commit
// resolves. gap runs in that work and rejectAfterAdopt makes a commit it matches fail there, durable and adopted, as a
// failed reclaim does; each fires once (session-support.ts LateResolvingStorage).
type lateResolvingStorage struct {
	*controlledStorage
	// committing is set from a commit's start until it resolves.
	committing atomic.Bool
	hookMu     sync.Mutex
	gap        func(writes []durable.StorageWrite) bool
	reject     func(writes []durable.StorageWrite) bool
}

func newLateResolvingStorage() *lateResolvingStorage {
	return &lateResolvingStorage{controlledStorage: newControlledStorage()}
}

func (s *lateResolvingStorage) setGap(gap func(writes []durable.StorageWrite) bool) {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	s.gap = gap
}

func (s *lateResolvingStorage) setRejectAfterAdopt(reject func(writes []durable.StorageWrite) bool) {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	s.reject = reject
}

func (s *lateResolvingStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	s.committing.Store(true)
	defer s.committing.Store(false)
	seq, err := s.controlledStorage.Commit(ctx, writes, adopt)
	if err != nil {
		return 0, err
	}
	time.Sleep(lateWork)
	s.hookMu.Lock()
	gap := s.gap
	s.hookMu.Unlock()
	if gap != nil && gap(writes) {
		s.hookMu.Lock()
		s.gap = nil
		s.hookMu.Unlock()
		// What the gap started runs before the commit resolves.
		time.Sleep(lateWork)
	}
	s.hookMu.Lock()
	reject := s.reject
	s.hookMu.Unlock()
	if reject != nil && reject(writes) {
		s.hookMu.Lock()
		s.reject = nil
		s.hookMu.Unlock()
		return 0, errReclaimFailed
	}
	return seq, nil
}

func (s *controlledStorage) Close(ctx context.Context) error {
	if err := s.MemoryStorage.Close(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeFailure
}

// documentChanges returns the document changes among the changes of a publication.
func documentChanges(publication []durable.CommitChange) []durable.DocumentChange {
	var changes []durable.DocumentChange
	for _, change := range publication {
		if document, ok := change.(durable.DocumentChange); ok {
			changes = append(changes, document)
		}
	}
	return changes
}

// observeCommits observes every commit as memory adopts it, deferred ones before they are stored, as views and document
// watches do; SubscribeCommits sees only what was stored (session-support.ts observeCommits).
func observeCommits(harness Harness, listener func(context.Context, session.ObservedCommit)) func() {
	return harness.(*harnessImpl).ObserveCommits(listener)
}
