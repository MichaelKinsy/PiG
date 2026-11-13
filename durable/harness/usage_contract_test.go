package harness

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// failingDocTx is a transaction whose pi.usage document cannot be opened.
type failingDocTx struct {
	durable.Tx
	opened []any
	err    error
}

func (tx *failingDocTx) Doc(_ durable.AnyDocToken, args ...any) (*delta.Object, error) {
	tx.opened = args
	return nil, tx.err
}

// usage.ts recordUsage awaits tx.doc(UsageDoc, conversationId) inside the commit that records the response and adds to the draft it returns; a document that
// cannot be opened is the failure of the recording itself, and nothing is added.
func TestRecordUsageOpensTheConversationLedgerAndFailsWithItsFailure(t *testing.T) {
	tx := &failingDocTx{err: errors.New("ledger unavailable")}
	err := RecordUsage(tx, 7, UsageModels, "p/m", ai.Usage{Input: 1})
	if !errors.Is(err, tx.err) {
		t.Fatalf("err = %v, want the ledger's failure", err)
	}
	if len(tx.opened) != 1 || tx.opened[0] != durable.ConversationId(7) {
		t.Fatalf("opened the ledger with %v, want the conversation 7", tx.opened)
	}
}
