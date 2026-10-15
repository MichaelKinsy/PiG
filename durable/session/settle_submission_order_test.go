package session_test

import (
	"errors"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// upstream: packages/durable/src/session/transaction.ts settleSubmission: this.#assertOpen(); this.#hasTableWrite = true; then copyJson(settlement).
// A settlement that is not strict JSON is rejected after the transaction counts a table write, so a later table read
// in the same commit is refused; on a settled transaction the settled error wins over the JSON check.
func TestSettleSubmissionChecksOpenAndMarksTheWriteBeforeCopying(t *testing.T) {
	harness := open()
	conversationId := createConversation(t, harness)
	var captured durable.Tx
	errReadAfterWrite := errors.New("read after a rejected settlement was allowed")
	err := tryCommit(harness.Session, func(tx durable.Tx) error {
		captured = tx
		expectStrictJSON(t, tx.SettleSubmission(1, durable.SubmissionSettlement{Status: durable.SubmissionDone, Detail: math.NaN()}))
		if _, err := tx.Conversation(conversationId); err == nil {
			return errReadAfterWrite
		} else {
			expectErrorContains(t, err, "cannot read tables after the first table write")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	expectErrorContains(t, captured.SettleSubmission(1, durable.SubmissionSettlement{Status: durable.SubmissionDone, Detail: math.NaN()}), "Transaction has settled")
}
