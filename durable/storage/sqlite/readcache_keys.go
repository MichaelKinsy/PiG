package sqlite

import (
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
)

// The cache key of each read: its arguments, as the read resolves them. A scan's cursor is its resolved start (order and
// last ID), so two cursors that continue a scan from the same place are one key.

func startFlags(start scan.Start) (flags uint32, after int64) {
	if start.Order == durable.ScanAscending {
		flags |= flagAscending
	}
	if start.HasAfter {
		flags |= flagHasAfter
	}
	return flags, start.After
}

func entryKey(id durable.EntryId) cacheKey { return cacheKey{kind: cacheEntry, a: int64(id)} }

func visibleEntryKey(conversationId durable.ConversationId, id durable.EntryId) cacheKey {
	return cacheKey{kind: cacheVisibleEntry, a: int64(conversationId), b: int64(id)}
}

func headMarkerKey(conversationId durable.ConversationId, atOrBefore *durable.EntryId) cacheKey {
	key := cacheKey{kind: cacheHeadMarker, a: int64(conversationId)}
	if atOrBefore != nil {
		key.flags |= flagHasB
		key.b = int64(*atOrBefore)
	}
	return key
}

func scanEntriesKey(query durable.EntryQuery, limit int, start scan.Start) cacheKey {
	key := cacheKey{kind: cacheScanEntries, a: int64(query.ConversationId), limit: limit}
	key.flags, key.d = startFlags(start)
	if query.MinEntryId != nil {
		key.flags |= flagHasB
		key.b = int64(*query.MinEntryId)
	}
	if query.MaxEntryId != nil {
		key.flags |= flagHasC
		key.c = int64(*query.MaxEntryId)
	}
	return key
}

func conversationKey(id durable.ConversationId) cacheKey {
	return cacheKey{kind: cacheConversation, a: int64(id)}
}

func scanConversationsKey(query durable.ConversationQuery, limit int, start scan.Start) cacheKey {
	key := cacheKey{kind: cacheScanConversations, limit: limit}
	key.flags, key.d = startFlags(start)
	if query.OwnerConversationId != nil {
		key.flags |= flagHasA
		key.a = int64(*query.OwnerConversationId)
	}
	if query.OwnerTaskId != nil {
		key.flags |= flagHasB
		key.b = int64(*query.OwnerTaskId)
	}
	return key
}

func taskKey(id durable.TaskId) cacheKey { return cacheKey{kind: cacheTask, a: int64(id)} }

func scanTasksKey(query durable.TaskQuery, limit int, start scan.Start) cacheKey {
	key := cacheKey{kind: cacheScanTasks, limit: limit}
	key.flags, key.d = startFlags(start)
	if query.ConversationId != nil {
		key.flags |= flagHasA
		key.a = int64(*query.ConversationId)
	}
	if query.Kind != nil {
		key.flags |= flagHasText
		key.s = *query.Kind
	}
	if query.Status != nil {
		key.flags |= flagHasOther
		key.t = string(*query.Status)
	}
	if query.AbortRequested != nil {
		key.flags |= flagAbortKnown
		if *query.AbortRequested {
			key.flags |= flagAbortRequested
		}
	}
	if query.Background != nil {
		key.flags |= flagBackgroundKnown
		if *query.Background {
			key.flags |= flagBackground
		}
	}
	return key
}

func submissionKey(id durable.SubmissionId) cacheKey {
	return cacheKey{kind: cacheSubmission, a: int64(id)}
}

func scanSubmissionsKey(query durable.SubmissionQuery, limit int, start scan.Start) cacheKey {
	key := cacheKey{kind: cacheScanSubmissions, limit: limit}
	key.flags, key.d = startFlags(start)
	if query.ConversationId != nil {
		key.flags |= flagHasA
		key.a = int64(*query.ConversationId)
	}
	if query.Status != nil {
		key.flags |= flagHasOther
		key.t = string(*query.Status)
	}
	return key
}

func submissionByRequestKey(conversationId durable.ConversationId, requestId string) cacheKey {
	return cacheKey{kind: cacheSubmissionByRequest, a: int64(conversationId), s: requestId}
}

// scopeFlags is the key bits and owner of a document scope; ok is false for a scope this package does not know.
func scopeFlags(scope durable.DocumentRecordScope) (flags uint32, owner int64, ok bool) {
	switch scope.Kind {
	case durable.ScopeSession:
		return 0, 0, true
	case durable.ScopeConversation:
		return flagScopeConversation, int64(scope.ConversationId), true
	case durable.ScopeTask:
		return flagScopeTask, int64(scope.TaskId), true
	default:
		return 0, 0, false
	}
}

func pointFlags(key *cacheKey, at durable.DocumentPoint) {
	if at.Current {
		key.flags |= flagCurrent
		return
	}
	key.c = int64(at.Seq)
}

// findDocumentKey is the key of FindDocument; ok is false for an address this package does not know.
func findDocumentKey(address durable.DocumentAddress, at durable.DocumentPoint) (key cacheKey, ok bool) {
	flags, owner, ok := scopeFlags(address.Scope)
	if !ok {
		return key, false
	}
	key = cacheKey{kind: cacheFindDocument, flags: flags, a: owner, s: address.Kind}
	if address.Key != nil {
		key.flags |= flagKeyed
		key.t = *address.Key
	}
	pointFlags(&key, at)
	return key, true
}

func documentKey(id durable.DocumentId, at durable.DocumentPoint) cacheKey {
	key := cacheKey{kind: cacheDocument, a: int64(id)}
	pointFlags(&key, at)
	return key
}

// scanDocumentsKey is the key of ScanDocuments; ok is false for a scope this package does not know.
func scanDocumentsKey(query durable.DocumentQuery, limit int, after int64) (key cacheKey, ok bool) {
	flags, owner, ok := scopeFlags(query.Scope)
	if !ok {
		return key, false
	}
	key = cacheKey{kind: cacheScanDocuments, flags: flags | flagAscending, a: owner, d: after, limit: limit}
	if query.Kind != nil {
		key.flags |= flagHasText
		key.s = *query.Kind
	}
	pointFlags(&key, query.At)
	return key, true
}
