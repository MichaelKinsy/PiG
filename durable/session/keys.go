// The keys of the Session line: what a commit writes and a job reads, so a read of an unpublished write waits for it.
//
// Ports packages/durable/src/session/keys.ts
//
// Go mapping decisions:
//   - Upstream's `touch` returns `Promise<void> | undefined`: a gate to wait on, or nothing. Here it is a blocking
//     call that returns when the key has no unpublished writer (at once when it never had one). The wait is not
//     cancellable, as upstream's: a gate always opens, by a publication or by the Session's failure (Session.AwaitTouch).
//   - `Storage.entry`'s two overloads are Entry and VisibleEntry, as the Go Storage names them.
//   - Upstream's `#after` chains the read on the gate's promise; here the read runs after the blocking touch.
package session

import (
	"context"
	"fmt"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable"
)

// TaskKey is a task record.
func TaskKey(id durable.TaskId) string {
	return "task:" + strconv.FormatInt(int64(id), 10)
}

// ConversationKey is a conversation record.
func ConversationKey(id durable.ConversationId) string {
	return "conv:" + strconv.FormatInt(int64(id), 10)
}

// EntryKey is an entry record.
func EntryKey(id durable.EntryId) string {
	return "entry:" + strconv.FormatInt(int64(id), 10)
}

// AppendKey is the newest entries of a conversation: appended blind, read by scans, the newest entry, and head
// markers.
func AppendKey(id durable.ConversationId) string {
	return "entry-append:" + strconv.FormatInt(int64(id), 10)
}

// SubmissionKey is a submission record.
func SubmissionKey(id durable.SubmissionId) string {
	return "sub:" + strconv.FormatInt(int64(id), 10)
}

// RequestKey is the submission a conversation's request ID names.
func RequestKey(conversationId durable.ConversationId, requestId string) string {
	return "req:" + strconv.FormatInt(int64(conversationId), 10) + ":" + requestId
}

// DocumentKey is a document address (`AddressId()`): its current incarnation, value, and held changes.
func DocumentKey(addressId string) string {
	return "doc:" + addressId
}

// IncarnationKey is a document incarnation, for reads by its ID.
func IncarnationKey(id durable.DocumentId) string {
	return "docid:" + strconv.FormatInt(int64(id), 10)
}

// ScopeKey is the incarnations alive in a scope: created and retired blind, read by scans and the task-document
// index.
func ScopeKey(scope durable.DocumentRecordScope) string {
	switch scope.Kind {
	case durable.ScopeSession:
		return "docscope:session"
	case durable.ScopeConversation:
		return "docscope:c" + strconv.FormatInt(int64(scope.ConversationId), 10)
	case durable.ScopeTask:
		return "docscope:t" + strconv.FormatInt(int64(scope.TaskId), 10)
	}
	panic(fmt.Sprintf("session: unknown document scope %q", scope.Kind))
}

// OwnedKey is a node of the scheduler's ownership tree (a task ID, or `c<conversation ID>`): the live work listed
// below it. Written by task creation and a task becoming terminal for every node from the task's parent up to and
// including the nearest live task or the root conversation; read by every walk that expands the node.
func OwnedKey[N durable.TaskId | string](node N) string {
	if id, ok := any(node).(durable.TaskId); ok {
		return "owned:" + strconv.FormatInt(int64(id), 10)
	}
	return "owned:" + any(node).(string)
}

const (
	// RootsKey is the ownerless conversations with live work below them, which global idle walks from.
	RootsKey = "roots"
	// IntentKey is the live tasks with cancellation intent: abort marks, and held outcomes other than `completed`.
	IntentKey = "intent"
	// AllKeys is every unpublished write: what a read that cannot be keyed, such as a scan, touches.
	AllKeys = "*"
)

// TableKeys appends the keys of the table records in writes to keys; document writes are keyed by the transaction,
// which knows addresses.
func TableKeys(writes []durable.StorageWrite, keys *[]string) {
	for _, write := range writes {
		switch write := write.(type) {
		case durable.ConversationWrite:
			*keys = append(*keys, ConversationKey(write.Value.Id))
		case durable.EntryWrite:
			*keys = append(*keys, EntryKey(write.Value.Id), AppendKey(write.Value.ConversationId))
		case durable.TaskWrite:
			*keys = append(*keys, TaskKey(write.Value.Id))
		case durable.SubmissionWrite:
			*keys = append(*keys, SubmissionKey(write.Value.Id))
			if write.Value.RequestId != nil {
				*keys = append(*keys, RequestKey(write.Value.ConversationId, *write.Value.RequestId))
			}
		}
	}
}

var _ durable.Storage = (*GatedStorage)(nil)

// GatedStorage is the Storage a job on the line reads through: every read first touches its key (`touch`), which
// waits while the key has an unpublished writer, so the read sees what the line admitted before it. Scans that cannot
// be keyed touch AllKeys. Commits and Close pass through; the line never commits through it.
type GatedStorage struct {
	storage durable.Storage
	touch   func(ctx context.Context, key string) error
}

// NewGatedStorage gates the reads of storage on touch, which blocks until the gate of the newest unpublished writer
// of key opened and returns nil at once when there is none.
func NewGatedStorage(storage durable.Storage, touch func(ctx context.Context, key string) error) *GatedStorage {
	return &GatedStorage{storage: storage, touch: touch}
}

// Commit passes through.
func (gated *GatedStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	return gated.storage.Commit(ctx, batches, receive)
}

// MintId passes through.
func (gated *GatedStorage) MintId() (int64, error) {
	return gated.storage.MintId()
}

// Conversation reads once the conversation has no unpublished writer.
func (gated *GatedStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if err := gated.touch(ctx, ConversationKey(id)); err != nil {
		return nil, err
	}
	return gated.storage.Conversation(ctx, id)
}

// ScanConversations reads once nothing is unpublished.
func (gated *GatedStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	if err := gated.touch(ctx, AllKeys); err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	return gated.storage.ScanConversations(ctx, query, limit, cursor)
}

// Entry reads once the entry has no unpublished writer.
func (gated *GatedStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	if err := gated.touch(ctx, EntryKey(id)); err != nil {
		return nil, err
	}
	return gated.storage.Entry(ctx, id)
}

// VisibleEntry reads once neither the conversation nor the entry has an unpublished writer: visibility follows the
// conversation's immutable ancestry, and the conversation may be unpublished itself.
func (gated *GatedStorage) VisibleEntry(ctx context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	if err := gated.touch(ctx, ConversationKey(conversationId)); err != nil {
		return nil, err
	}
	if err := gated.touch(ctx, EntryKey(id)); err != nil {
		return nil, err
	}
	return gated.storage.VisibleEntry(ctx, conversationId, id)
}

// FindLatestHeadMarker reads once the conversation's appends and creation have no unpublished writer.
func (gated *GatedStorage) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId) (*durable.EntryRecord, error) {
	if err := gated.afterEntries(ctx, conversationId); err != nil {
		return nil, err
	}
	return gated.storage.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
}

// ScanEntries reads once the conversation's appends and creation have no unpublished writer.
func (gated *GatedStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	if err := gated.afterEntries(ctx, query.ConversationId); err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	return gated.storage.ScanEntries(ctx, query, limit, cursor)
}

// Task reads once the task has no unpublished writer.
func (gated *GatedStorage) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	if err := gated.touch(ctx, TaskKey(id)); err != nil {
		return nil, err
	}
	return gated.storage.Task(ctx, id)
}

// ScanTasks reads once nothing is unpublished.
func (gated *GatedStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
	if err := gated.touch(ctx, AllKeys); err != nil {
		return durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor]{}, err
	}
	return gated.storage.ScanTasks(ctx, query, limit, cursor)
}

// Submission reads once the submission has no unpublished writer.
func (gated *GatedStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	if err := gated.touch(ctx, SubmissionKey(id)); err != nil {
		return nil, err
	}
	return gated.storage.Submission(ctx, id)
}

// ScanSubmissions reads once nothing is unpublished.
func (gated *GatedStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if err := gated.touch(ctx, AllKeys); err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	return gated.storage.ScanSubmissions(ctx, query, limit, cursor)
}

// SubmissionByRequest reads once the request key has no unpublished writer.
func (gated *GatedStorage) SubmissionByRequest(ctx context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	if err := gated.touch(ctx, RequestKey(conversationId, requestId)); err != nil {
		return nil, err
	}
	return gated.storage.SubmissionByRequest(ctx, conversationId, requestId)
}

// FindDocument reads once the address has no unpublished writer.
func (gated *GatedStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	if err := gated.touch(ctx, DocumentKey(durable.AddressId(address))); err != nil {
		return nil, err
	}
	return gated.storage.FindDocument(ctx, address, at)
}

// Document reads once the incarnation has no unpublished writer.
func (gated *GatedStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	if err := gated.touch(ctx, IncarnationKey(id)); err != nil {
		return nil, err
	}
	return gated.storage.Document(ctx, id, at)
}

// ScanDocuments reads once the scope has no unpublished writer.
func (gated *GatedStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	if err := gated.touch(ctx, ScopeKey(query.Scope)); err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	return gated.storage.ScanDocuments(ctx, query, limit, cursor)
}

// Close passes through.
func (gated *GatedStorage) Close(ctx context.Context) error {
	return gated.storage.Close(ctx)
}

// afterEntries waits for a read of a conversation's entries until neither its appends nor its creation has an
// unpublished writer: Storage refuses the read of a conversation it does not have yet.
func (gated *GatedStorage) afterEntries(ctx context.Context, conversationId durable.ConversationId) error {
	if err := gated.touch(ctx, ConversationKey(conversationId)); err != nil {
		return err
	}
	return gated.touch(ctx, AppendKey(conversationId))
}
