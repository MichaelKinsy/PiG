package sqlite_test

import (
	"errors"
	"fmt"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Pi sqlite/storage.ts:722-729 and 860-868: a document.copy whose source is changed in the same commit is a StorageRejected
// "Document copy <id> source is changed in the copy batch" with no cause; a copy whose source cannot be resolved is a StorageRejected
// "Document copy <id> was rejected" wrapping the failure, and a StorageRejected passes through unwrapped. Either way the commit is rejected
// before any durable effect (errors.ts:11): nothing is stored and the next commit proceeds.
func TestSqliteStorageRejectsAnUnresolvableDocumentCopyBeforeAnyEffect(t *testing.T) {
	storage, _ := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
	createRoot(t, storage)
	other := durable.ConversationId(2)
	commit(t, storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: other}})
	record := func(id durable.DocumentId, conversation durable.ConversationId) durable.DocumentCreate {
		return durable.DocumentCreate{Id: id, Kind: "doc", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversation}}
	}
	source := mint[durable.DocumentId](t, storage)
	commit(t, storage, durable.DocumentCreateWrite{Record: record(source, durable.ROOT_CONVERSATION_ID), Content: base(chorddelta.JsonObjectOf("value", "source"))})

	expectRejected := func(name string, writes []durable.StorageWrite, message string, hasCause bool) {
		t.Helper()
		_, err := storage.Commit(testContext, writes)
		rejected, ok := errors.AsType[*durable.StorageRejected](err)
		if !ok {
			t.Fatalf("%s: error = %v (%T), want *StorageRejected", name, err, err)
		}
		if rejected.Message != message {
			t.Errorf("%s: message = %q, want %q", name, rejected.Message, message)
		}
		if (errors.Unwrap(rejected) != nil) != hasCause {
			t.Errorf("%s: cause = %v, want a cause: %v", name, errors.Unwrap(rejected), hasCause)
		}
	}

	copied := mint[durable.DocumentId](t, storage)
	expectRejected("source changed in the batch", []durable.StorageWrite{
		durable.DocumentChangeWrite{Id: source, Content: base(chorddelta.JsonObjectOf("value", "changed"))},
		durable.DocumentCopyWrite{Record: record(copied, other), Source: durable.DocumentCopySource{Id: source, At: durable.CurrentPoint}},
	}, fmt.Sprintf("Document copy %d source is changed in the copy batch", copied), false)

	missing := durable.DocumentId(source + 1000)
	expectRejected("unreadable source", []durable.StorageWrite{
		durable.DocumentCopyWrite{Record: record(copied, other), Source: durable.DocumentCopySource{Id: missing, At: durable.CurrentPoint}},
	}, fmt.Sprintf("Document copy %d was rejected", copied), true)

	if found := must(storage.FindDocument(testContext, durable.DocumentAddress{Kind: "doc", Scope: record(copied, other).Scope}, durable.CurrentPoint)); found != nil {
		t.Fatalf("a rejected copy stored %+v", found)
	}
	// The copy that resolves is admitted: the rejections are the copy checks, not a blanket failure.
	commit(t, storage, durable.DocumentCopyWrite{Record: record(copied, other), Source: durable.DocumentCopySource{Id: source, At: durable.CurrentPoint}})
}
