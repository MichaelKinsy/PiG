package session

// Ports packages/durable/src/session/session.ts GuardedStorage.

import (
	"context"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/entryscan"
)

// failureLatch is the Session's failure state, which the guard reads and sets.
type failureLatch interface {
	failure() error
	fail(error)
}

// guardedStorage is the Session's Storage behind its failure guard; every component of the Session reads and writes
// through it. Before a call, a failed Session gets SessionFailed without reaching the backend. A call that fails fails
// the Session and returns the backend's error, unless it is a StorageRequestError or a read whose caller's context was
// cancelled: neither says the Storage is broken. Close always reaches the backend, so a failed Session still closes it.
type guardedStorage struct {
	storage durable.Storage
	latch   failureLatch

	mu      sync.Mutex
	closing bool
	// underway counts the backend calls underway, also those off the Session line, which Close waits for before it closes
	// the backend.
	underway sync.WaitGroup
}

func newGuardedStorage(storage durable.Storage, latch failureLatch) *guardedStorage {
	return &guardedStorage{storage: storage, latch: latch}
}

// rangedGuard is the guard of a backend that serves visible entry ranges: the Harness reads them when the Storage offers
// them, so the guard offers them exactly when the backend does.
type rangedGuard struct {
	*guardedStorage
	ranger entryscan.Ranger
}

// VisibleEntryRange returns the visible entries within query's bounds, under the guard.
func (guard rangedGuard) VisibleEntryRange(ctx context.Context, query durable.EntryQuery) ([]durable.EntryRecord, error) {
	return guardedCall(guard.guardedStorage, ctx, func() ([]durable.EntryRecord, error) { return guard.ranger.VisibleEntryRange(ctx, query) })
}

// exposed returns the guard as a Storage that offers the optional capabilities of its backend.
func (guard *guardedStorage) exposed() durable.Storage {
	if ranger, ok := guard.storage.(entryscan.Ranger); ok {
		return rangedGuard{guardedStorage: guard, ranger: ranger}
	}
	return guard
}

func (guard *guardedStorage) assertHealthy() error {
	if failure := guard.latch.failure(); failure != nil {
		return durable.NewSessionFailed(failure)
	}
	return nil
}

// guardedCall runs one backend call under the guard. read is a read's context; Commit and MintId pass none, so nothing
// exempts their errors: once a batch is admitted, whether it committed is unknown. A call still underway when another
// fails the Session ends with SessionFailed too, whatever it returns: nothing it read or wrote is used.
func guardedCall[T any](guard *guardedStorage, read context.Context, run func() (T, error)) (T, error) {
	var zero T
	if err := guard.assertHealthy(); err != nil {
		return zero, err
	}
	guard.mu.Lock()
	if guard.closing {
		guard.mu.Unlock()
		return zero, ErrSessionClosed
	}
	guard.underway.Add(1)
	guard.mu.Unlock()
	defer guard.underway.Done()
	result, err := run()
	if err != nil {
		if failed := guard.assertHealthy(); failed != nil {
			return zero, failed
		}
		// An invalid read, or one its caller cancelled, says nothing about the Storage; it fails that call only.
		_, invalid := errors.AsType[*durable.StorageRequestError](err)
		exempt := read != nil && (invalid || read.Err() != nil)
		if !exempt {
			guard.latch.fail(err)
		}
		return zero, err
	}
	if err := guard.assertHealthy(); err != nil {
		return zero, err
	}
	return result, nil
}

// Commit is never exempt, whatever it fails with.
func (guard *guardedStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	return guardedCall(guard, nil, func() (durable.Seq, error) { return guard.storage.Commit(ctx, writes) })
}

func (guard *guardedStorage) MintId() (int64, error) {
	return guardedCall(guard, nil, func() (int64, error) { return guard.storage.MintId() })
}

func (guard *guardedStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	return guardedCall(guard, ctx, func() (*durable.ConversationRecord, error) { return guard.storage.Conversation(ctx, id) })
}

func (guard *guardedStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	return guardedCall(guard, ctx, func() (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
		return guard.storage.ScanConversations(ctx, query, limit, cursor)
	})
}

func (guard *guardedStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	return guardedCall(guard, ctx, func() (*durable.EntryAt, error) { return guard.storage.Entry(ctx, id) })
}

func (guard *guardedStorage) VisibleEntry(ctx context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	return guardedCall(guard, ctx, func() (*durable.EntryAt, error) { return guard.storage.VisibleEntry(ctx, conversationId, id) })
}

func (guard *guardedStorage) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId) (*durable.EntryRecord, error) {
	return guardedCall(guard, ctx, func() (*durable.EntryRecord, error) {
		return guard.storage.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
	})
}

func (guard *guardedStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return guardedCall(guard, ctx, func() (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return guard.storage.ScanEntries(ctx, query, limit, cursor)
	})
}

func (guard *guardedStorage) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	return guardedCall(guard, ctx, func() (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
		return guard.storage.Task(ctx, id)
	})
}

func (guard *guardedStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
	return guardedCall(guard, ctx, func() (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return guard.storage.ScanTasks(ctx, query, limit, cursor)
	})
}

func (guard *guardedStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	return guardedCall(guard, ctx, func() (*durable.SubmissionRecord, error) { return guard.storage.Submission(ctx, id) })
}

func (guard *guardedStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	return guardedCall(guard, ctx, func() (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
		return guard.storage.ScanSubmissions(ctx, query, limit, cursor)
	})
}

func (guard *guardedStorage) SubmissionByRequest(ctx context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	return guardedCall(guard, ctx, func() (*durable.SubmissionRecord, error) {
		return guard.storage.SubmissionByRequest(ctx, conversationId, requestId)
	})
}

func (guard *guardedStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	return guardedCall(guard, ctx, func() (*durable.DocumentRecord, error) { return guard.storage.FindDocument(ctx, address, at) })
}

func (guard *guardedStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	return guardedCall(guard, ctx, func() (*durable.StoredDocument, error) { return guard.storage.Document(ctx, id, at) })
}

func (guard *guardedStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	return guardedCall(guard, ctx, func() (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
		return guard.storage.ScanDocuments(ctx, query, limit, cursor)
	})
}

// Close closes the backend once every call underway has settled, so none outlives it or fails after the Session's end
// settles. New calls are refused from here on: a paged read must not start its next page while the backend closes.
func (guard *guardedStorage) Close(ctx context.Context) error {
	guard.mu.Lock()
	guard.closing = true
	guard.mu.Unlock()
	guard.underway.Wait()
	return guard.storage.Close(ctx)
}
