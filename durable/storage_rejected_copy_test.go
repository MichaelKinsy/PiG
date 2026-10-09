package durable_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func conversationDocumentWrite(id durable.DocumentId, kind string) durable.DocumentCreateWrite {
	return durable.DocumentCreateWrite{
		Record:  durable.DocumentCreate{Id: id, Kind: kind, Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID}},
		Content: durable.DocumentContent{Version: 1, Kind: durable.ContentBase, Value: delta.JsonObjectOf("value", "source")},
	}
}

func rejectedCopy(t *testing.T, err error, message, cause string) {
	t.Helper()
	rejected, ok := errors.AsType[*durable.StorageRejected](err)
	if !ok {
		t.Fatalf("error = %v (%T), want *StorageRejected", err, err)
	}
	if rejected.Message != message {
		t.Errorf("message = %q, want %q", rejected.Message, message)
	}
	if inner := errors.Unwrap(rejected); inner == nil || inner.Error() != cause {
		t.Errorf("cause = %v, want %q", inner, cause)
	}
}

// Pi memory.ts:290-326 resolveDocumentCopies: a document.copy whose source is changed in the same batch, cannot be read, or does not match the
// copied record throws a StorageRejected "Document copy <id> was rejected" with the failure as its cause; a StorageRejected passes through
// unwrapped. The rejection happens before any durable effect (errors.ts:11, "the owning Session may continue safely"): no sequence is
// consumed and nothing is stored.
func TestMemoryStorageRejectsAnUnresolvableDocumentCopyBeforeAnyEffect(t *testing.T) {
	memory := storage.NewMemoryStorage()
	setup := []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 2}},
		conversationDocumentWrite(5, "doc"),
	}
	if _, err := memory.Commit(t.Context(), setup); err != nil {
		t.Fatal(err)
	}
	source := durable.DocumentCopySource{Id: 5, At: durable.CurrentPoint}
	// The copy lands in another conversation, as a fork's does, so its address is free.
	copyOf := func(id durable.DocumentId, kind string, from durable.DocumentCopySource) durable.StorageWrite {
		return durable.DocumentCopyWrite{
			Record: durable.DocumentCreate{Id: id, Kind: kind, Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 2}},
			Source: from,
		}
	}
	next := func() durable.Seq {
		prepared, err := memory.PrepareCommit(nil)
		if err != nil {
			t.Fatal(err)
		}
		return prepared.Seq()
	}
	before := next()

	_, err := memory.PrepareCommit([]durable.StorageWrite{conversationDocumentWrite(8, "other"), copyOf(9, "other", durable.DocumentCopySource{Id: 8})})
	rejectedCopy(t, err, "Document copy 9 was rejected", "Fork source document 8 is changed in the copy batch")

	_, err = memory.PrepareCommit([]durable.StorageWrite{copyOf(6, "doc", durable.DocumentCopySource{Id: 99})})
	rejectedCopy(t, err, "Document copy 6 was rejected", "Fork source document 99 cannot be read")

	_, err = memory.PrepareCommit([]durable.StorageWrite{copyOf(6, "different-kind", source)})
	rejectedCopy(t, err, "Document copy 6 was rejected", "Fork source document 5 does not match the copied record")

	if after := next(); after != before {
		t.Fatalf("a rejected commit consumed sequence %d (next was %d)", after, before)
	}
	// A copy that resolves is admitted, so the rejections above are the copy checks and not a blanket failure.
	prepared, err := memory.PrepareCommit([]durable.StorageWrite{copyOf(6, "doc", source)})
	if err != nil {
		t.Fatalf("a matching copy: %v", err)
	}
	prepared.Apply()
}
