// Package sessiontest holds the shared Session test support: a controlled
// memory storage and an opened Session kernel with its publications.
//
// Ports packages/durable/test/session-support.ts
package sessiontest

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Gate holds calls until released and reports when the first held call arrives.
type Gate struct {
	entered     chan struct{}
	enteredOnce sync.Once
	gate        chan struct{}
	releaseOnce sync.Once
	clear       func(*Gate)
}

func newGate(clear func(*Gate)) *Gate {
	return &Gate{entered: make(chan struct{}), gate: make(chan struct{}), clear: clear}
}

// Entered is closed when the first held call arrives.
func (gate *Gate) Entered() <-chan struct{} { return gate.entered }

// Release lets every held call proceed and stops holding later calls.
func (gate *Gate) Release() {
	gate.releaseOnce.Do(func() {
		gate.clear(gate)
		close(gate.gate)
	})
}

func (gate *Gate) hold() {
	gate.enteredOnce.Do(func() { close(gate.entered) })
	<-gate.gate
}

// ControlledStorage is memory storage with observable commits, held calls, and injected commit failures.
type ControlledStorage struct {
	*storage.MemoryStorage

	mu                sync.Mutex
	admittedCommits   [][]durable.StorageWrite
	commits           [][]durable.StorageWrite
	mintCount         int
	documentReadCount int
	commitGate        *Gate
	findGate          *Gate
	commitFailure     error
}

// NewControlledStorage returns an empty controlled memory storage.
func NewControlledStorage() *ControlledStorage {
	return &ControlledStorage{MemoryStorage: storage.NewMemoryStorage()}
}

// AdmittedCommits returns the exact batches the Session admitted.
func (controlled *ControlledStorage) AdmittedCommits() [][]durable.StorageWrite {
	controlled.mu.Lock()
	defer controlled.mu.Unlock()
	return slices.Clone(controlled.admittedCommits)
}

// Commits returns detached copies of the admitted batches for value assertions.
func (controlled *ControlledStorage) Commits() [][]durable.StorageWrite {
	controlled.mu.Lock()
	defer controlled.mu.Unlock()
	return slices.Clone(controlled.commits)
}

// LastCommit returns the newest admitted batch.
func (controlled *ControlledStorage) LastCommit() []durable.StorageWrite {
	commits := controlled.Commits()
	if len(commits) == 0 {
		return nil
	}
	return commits[len(commits)-1]
}

// MintCount returns the number of minted IDs.
func (controlled *ControlledStorage) MintCount() int {
	controlled.mu.Lock()
	defer controlled.mu.Unlock()
	return controlled.mintCount
}

// DocumentReadCount returns the number of document materializations.
func (controlled *ControlledStorage) DocumentReadCount() int {
	controlled.mu.Lock()
	defer controlled.mu.Unlock()
	return controlled.documentReadCount
}

// HoldCommits holds every commit until the returned gate is released.
func (controlled *ControlledStorage) HoldCommits() *Gate {
	gate := newGate(func(gate *Gate) {
		controlled.mu.Lock()
		if controlled.commitGate == gate {
			controlled.commitGate = nil
		}
		controlled.mu.Unlock()
	})
	controlled.mu.Lock()
	controlled.commitGate = gate
	controlled.mu.Unlock()
	return gate
}

// HoldFindDocument holds every document lookup until the returned gate is released.
func (controlled *ControlledStorage) HoldFindDocument() *Gate {
	gate := newGate(func(gate *Gate) {
		controlled.mu.Lock()
		if controlled.findGate == gate {
			controlled.findGate = nil
		}
		controlled.mu.Unlock()
	})
	controlled.mu.Lock()
	controlled.findGate = gate
	controlled.mu.Unlock()
	return gate
}

// Crash simulates a crash during the held commit: it never reaches storage, and later commits proceed.
func (controlled *ControlledStorage) Crash() {
	controlled.mu.Lock()
	controlled.commitGate = nil
	controlled.mu.Unlock()
}

// FailNextCommit makes the next commit fail with err.
func (controlled *ControlledStorage) FailNextCommit(err error) {
	controlled.mu.Lock()
	controlled.commitFailure = err
	controlled.mu.Unlock()
}

// Commit records the batch, honors a held gate and an injected failure, then commits.
func (controlled *ControlledStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	controlled.mu.Lock()
	controlled.admittedCommits = append(controlled.admittedCommits, writes)
	controlled.commits = append(controlled.commits, slices.Clone(writes))
	held := controlled.commitGate
	controlled.mu.Unlock()
	if held != nil {
		held.hold()
	}
	controlled.mu.Lock()
	failure := controlled.commitFailure
	controlled.commitFailure = nil
	controlled.mu.Unlock()
	if failure != nil {
		return 0, failure
	}
	return controlled.MemoryStorage.Commit(ctx, writes)
}

// MintId counts and mints.
func (controlled *ControlledStorage) MintId() (int64, error) {
	controlled.mu.Lock()
	controlled.mintCount++
	controlled.mu.Unlock()
	return controlled.MemoryStorage.MintId()
}

// Document counts and materializes.
func (controlled *ControlledStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	controlled.mu.Lock()
	controlled.documentReadCount++
	controlled.mu.Unlock()
	return controlled.MemoryStorage.Document(ctx, id, at)
}

// FindDocument honors a held gate, then resolves.
func (controlled *ControlledStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	controlled.mu.Lock()
	held := controlled.findGate
	controlled.mu.Unlock()
	if held != nil {
		held.hold()
	}
	return controlled.MemoryStorage.FindDocument(ctx, address, at)
}

// Publications collects every committed publication.
type Publications struct {
	mu    sync.Mutex
	items []durable.CommitPublication
}

// All returns the publications so far.
func (publications *Publications) All() []durable.CommitPublication {
	publications.mu.Lock()
	defer publications.mu.Unlock()
	return slices.Clone(publications.items)
}

// Len returns the number of publications.
func (publications *Publications) Len() int { return len(publications.All()) }

// Last returns the newest publication.
func (publications *Publications) Last() durable.CommitPublication {
	all := publications.All()
	return all[len(all)-1]
}

// Harness is a Session kernel plus its controlled storage and every committed publication.
type Harness struct {
	Storage      *ControlledStorage
	Session      *session.SessionImpl
	Publications *Publications
}

// OpenTestSession opens a Session kernel over a controlled storage.
func OpenTestSession() Harness {
	controlled := NewControlledStorage()
	kernel := session.CreateSession(controlled)
	publications := &Publications{}
	kernel.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		publications.mu.Lock()
		publications.items = append(publications.items, publication)
		publications.mu.Unlock()
	})
	return Harness{Storage: controlled, Session: kernel, Publications: publications}
}

// DocumentChanges returns the document changes of a publication.
func DocumentChanges(publication durable.CommitPublication) []durable.DocumentChange {
	var changes []durable.DocumentChange
	for _, change := range publication.Changes {
		if document, ok := change.(durable.DocumentChange); ok {
			changes = append(changes, document)
		}
	}
	return changes
}

// DocumentCopyChanges returns the document copy changes of a publication.
func DocumentCopyChanges(publication durable.CommitPublication) []durable.DocumentCopyChange {
	var changes []durable.DocumentCopyChange
	for _, change := range publication.Changes {
		if document, ok := change.(durable.DocumentCopyChange); ok {
			changes = append(changes, document)
		}
	}
	return changes
}

// CreateConversation creates one ownerless conversation and returns its ID.
func CreateConversation(t testing.TB, kernel *session.SessionImpl) durable.ConversationId {
	t.Helper()
	id, err := durable.Commit(context.Background(), kernel, func(tx durable.Tx) (durable.ConversationId, error) {
		record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return record.Id, err
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return id
}

// Flush waits until every scheduled watch and state delivery drained, as upstream's flush waits one macrotask.
func Flush(kernel *session.SessionImpl) { kernel.WaitDeliveries() }
