// pi: packages/durable/src/storage/memory.ts
package storage_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// memory.ts:122-127 (cursorId): `after` must be a safe integer. A JSON cursor carries its number as a float64, so an integral float
// is a valid position after that ID and a fractional one fails with "Invalid storage cursor". ScanDocuments decodes its cursor through
// this check (the other scans share scan.StartOf).
func TestMemoryStorageDocumentCursorAfterMustBeASafeInteger(t *testing.T) {
	store := storage.NewMemoryStorage()
	raCreateRoot(t, store)
	first, second := raMint[durable.DocumentId](t, store), raMint[durable.DocumentId](t, store)
	member := func(id durable.DocumentId, key string) durable.DocumentCreateWrite {
		return durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: id, Kind: "fam", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
			Content: raBase(delta.JsonObjectOf("n", 1.0)),
		}
	}
	raCommit(t, store, member(first, "k1"), member(second, "k2"))
	query := durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}

	after := raMust(store.ScanDocuments(raCtx, query, 10, durable.Cursor{"after": float64(first)}))
	if len(after.Items) != 1 || after.Items[0].Id != second {
		t.Fatalf("integral float cursor: items %+v, want only the document after the first", after.Items)
	}
	_, err := store.ScanDocuments(raCtx, query, 10, durable.Cursor{"after": float64(first) + 0.5})
	if err == nil || !strings.Contains(err.Error(), "Invalid storage cursor") {
		t.Fatalf("fractional cursor: error %v, want Invalid storage cursor", err)
	}
}

// memory.ts:395-398: replacing a submission's request ID removes only that request from the conversation's index; the conversation's other
// requests stay findable.
func TestMemoryStorageSubmissionRequestReplacementKeepsOtherRequests(t *testing.T) {
	store := storage.NewMemoryStorage()
	raCreateRoot(t, store)
	first, second := raMint[durable.SubmissionId](t, store), raMint[durable.SubmissionId](t, store)
	a, b, c := "req-a", "req-b", "req-c"
	submission := func(id durable.SubmissionId, request *string) durable.SubmissionWrite {
		return durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: request, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}}
	}
	raCommit(t, store, submission(first, &a), submission(second, &b))
	raCommit(t, store, submission(first, &c))

	if found := raMust(store.SubmissionByRequest(raCtx, durable.ROOT_CONVERSATION_ID, b)); found == nil || found.Id != second {
		t.Fatalf("request %q after another request was replaced: %+v, want submission %d", b, found, second)
	}
	if found := raMust(store.SubmissionByRequest(raCtx, durable.ROOT_CONVERSATION_ID, c)); found == nil || found.Id != first {
		t.Fatalf("replacement request %q: %+v, want submission %d", c, found, first)
	}
	gone, err := store.SubmissionByRequest(raCtx, durable.ROOT_CONVERSATION_ID, a)
	raExpectNil(t, gone, err)
}

// memory.ts:666 (materializeDocument): a historical read keeps the revisions whose seq is at most the point, so a read at exactly a revision's
// commit seq includes it.
func TestMemoryStorageDocumentReadAtARevisionSeqIncludesIt(t *testing.T) {
	store := storage.NewMemoryStorage()
	raCreateRoot(t, store)
	doc := raMint[durable.DocumentId](t, store)
	scope := durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID}
	created := raCommit(t, store, durable.DocumentCreateWrite{
		Record:  durable.DocumentCreate{Id: doc, Kind: "note", Scope: scope, History: durable.HistoryRewindable},
		Content: raBase(delta.JsonObjectOf("n", 1.0)),
	})
	changed := raCommit(t, store, durable.DocumentChangeWrite{Id: doc, Content: raBase(delta.JsonObjectOf("n", 2.0))})
	value := func(at durable.DocumentPoint) any {
		stored := raMust(store.Document(raCtx, doc, at))
		if stored == nil {
			t.Fatalf("document absent at %+v", at)
		}
		return stored.Value.Value("n")
	}
	if got := value(durable.DocumentPoint{Seq: created}); got != 1.0 {
		t.Fatalf("read at the creating seq = %v, want 1", got)
	}
	if got := value(durable.DocumentPoint{Seq: changed}); got != 2.0 {
		t.Fatalf("read at the changing seq = %v, want 2", got)
	}
}
