package session

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// appendEntry takes ownership of the record through copyJson (session/transaction.ts:368), which keeps a present empty `edits` list.
func TestCopyEntryRecordKeepsAPresentEmptyEditList(t *testing.T) {
	copied, err := copyEntryRecord(durable.EntryRecord{Id: 3, ConversationId: 1, Kind: "k", Edits: []durable.ContextEdit{}})
	if err != nil {
		t.Fatal(err)
	}
	if copied.Edits == nil || len(copied.Edits) != 0 {
		t.Fatalf("copied edits = %#v, want a present empty list", copied.Edits)
	}
	copied, err = copyEntryRecord(durable.EntryRecord{Id: 3, ConversationId: 1, Kind: "k"})
	if err != nil || copied.Edits != nil {
		t.Fatalf("copied edits = %#v, %v; want absent", copied.Edits, err)
	}
}
