package sqlite

import (
	"context"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
)

// The reads of a Storage, each answered from the read cache when it holds the result and from the database, through the
// uncached form, when it does not (readcache.go).

// Conversation looks up one conversation by exact ID.
func (storage *SqliteStorage) Conversation(
	ctx context.Context, id durable.ConversationId,
) (*durable.ConversationRecord, error) {
	return readThrough(storage, conversationKey(id), dependsOnConversations, cloneConversationRecord, sizeConversationRecord,
		func() (*durable.ConversationRecord, error) { return storage.uncachedConversation(ctx, id) })
}

// ScanConversations scans conversations by ID in query.Order, ascending by default.
func (storage *SqliteStorage) ScanConversations(
	ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	read := func() (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
		return storage.uncachedScanConversations(ctx, query, limit, cursor)
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return read()
	}
	return readThrough(storage, scanConversationsKey(query, limit, start), dependsOnConversations, cloneConversationPage, sizeConversationPage, read)
}

// Entry looks up one global entry and the sequence of the commit that persisted it.
func (storage *SqliteStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	return readThrough(storage, entryKey(id), dependsOnEntries, cloneEntryAt, sizeEntryAt,
		func() (*durable.EntryAt, error) { return storage.uncachedEntry(ctx, id) })
}

// VisibleEntry looks up one entry only when it is visible through the requested conversation's ancestry.
func (storage *SqliteStorage) VisibleEntry(
	ctx context.Context, conversationId durable.ConversationId, id durable.EntryId,
) (*durable.EntryAt, error) {
	return readThrough(storage, visibleEntryKey(conversationId, id), dependsOnConversations|dependsOnEntries, cloneEntryAt, sizeEntryAt,
		func() (*durable.EntryAt, error) { return storage.uncachedVisibleEntry(ctx, conversationId, id) })
}

// FindLatestHeadMarker returns the newest visible entry with a head at or below the optional inclusive cutoff.
func (storage *SqliteStorage) FindLatestHeadMarker(
	ctx context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	return readThrough(storage, headMarkerKey(conversationId, atOrBeforeEntryId), dependsOnConversations|dependsOnEntries, cloneEntryRecord, sizeEntryRecord,
		func() (*durable.EntryRecord, error) {
			return storage.uncachedFindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
		})
}

// ScanEntries scans the inclusive visible range in query.Order (descending by default), returning at most limit
// entries.
func (storage *SqliteStorage) ScanEntries(
	ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	read := func() (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return storage.uncachedScanEntries(ctx, query, limit, cursor)
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanDescending)
	if err != nil {
		return read()
	}
	return readThrough(storage, scanEntriesKey(query, limit, start), dependsOnConversations|dependsOnEntries, cloneEntryPage, sizeEntryPage, read)
}

// Task looks up the latest complete record for one task.
func (storage *SqliteStorage) Task(ctx context.Context, id durable.TaskId) (*storedTask, error) {
	return readThrough(storage, taskKey(id), dependsOnTasks, cloneTaskRecord, sizeTaskRecord,
		func() (*storedTask, error) { return storage.uncachedTask(ctx, id) })
}

// ScanTasks scans task records matching every supplied filter by ID in query.Order, ascending by default.
func (storage *SqliteStorage) ScanTasks(
	ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor,
) (durable.Page[storedTask, durable.Cursor], error) {
	read := func() (durable.Page[storedTask, durable.Cursor], error) {
		return storage.uncachedScanTasks(ctx, query, limit, cursor)
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return read()
	}
	return readThrough(storage, scanTasksKey(query, limit, start), dependsOnTasks, cloneTaskPage, sizeTaskPage, read)
}

// Submission looks up the latest complete record for one admitted submission.
func (storage *SqliteStorage) Submission(
	ctx context.Context, id durable.SubmissionId,
) (*durable.SubmissionRecord, error) {
	return readThrough(storage, submissionKey(id), dependsOnSubmissions, cloneSubmissionRecord, sizeSubmissionRecord,
		func() (*durable.SubmissionRecord, error) { return storage.uncachedSubmission(ctx, id) })
}

// ScanSubmissions scans submissions matching every supplied filter by ID in query.Order, ascending by default.
func (storage *SqliteStorage) ScanSubmissions(
	ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	read := func() (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
		return storage.uncachedScanSubmissions(ctx, query, limit, cursor)
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return read()
	}
	return readThrough(storage, scanSubmissionsKey(query, limit, start), dependsOnSubmissions, cloneSubmissionPage, sizeSubmissionPage, read)
}

// SubmissionByRequest finds a submission by its conversation-scoped host deduplication key.
func (storage *SqliteStorage) SubmissionByRequest(
	ctx context.Context, conversationId durable.ConversationId, requestId string,
) (*durable.SubmissionRecord, error) {
	return readThrough(storage, submissionByRequestKey(conversationId, requestId), dependsOnSubmissions, cloneSubmissionRecord, sizeSubmissionRecord,
		func() (*durable.SubmissionRecord, error) {
			return storage.uncachedSubmissionByRequest(ctx, conversationId, requestId)
		})
}

// FindDocument resolves the incarnation occupying one exact logical address at the selected point.
func (storage *SqliteStorage) FindDocument(
	ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint,
) (*durable.DocumentRecord, error) {
	read := func() (*durable.DocumentRecord, error) { return storage.uncachedFindDocument(ctx, address, at) }
	key, ok := findDocumentKey(address, at)
	if !ok {
		return read()
	}
	return readThrough(storage, key, dependsOnDocuments, cloneDocumentRecord, sizeDocumentRecord, read)
}

// Document materializes one specific incarnation by ID at the selected point without following a replacement at its
// address.
func (storage *SqliteStorage) Document(
	ctx context.Context, id durable.DocumentId, at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	return readThrough(storage, documentKey(id, at), dependsOnDocuments, cloneStoredDocument, sizeStoredDocument,
		func() (*durable.StoredDocument, error) { return storage.uncachedDocument(ctx, id, at) })
}

// ScanDocuments scans incarnations alive in one exact scope at the selected point.
func (storage *SqliteStorage) ScanDocuments(
	ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	read := func() (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
		return storage.uncachedScanDocuments(ctx, query, limit, cursor)
	}
	after, err := cursorStart(cursor)
	if err != nil {
		return read()
	}
	key, ok := scanDocumentsKey(query, limit, after)
	if !ok {
		return read()
	}
	return readThrough(storage, key, dependsOnDocuments, cloneDocumentPage, sizeDocumentPage, read)
}
