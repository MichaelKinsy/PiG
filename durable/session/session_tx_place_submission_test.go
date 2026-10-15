package session_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/session/transaction.ts:452-456 placeSubmission: it is a table write (a read after it throws ReadAfterWrite naming the read) and it is refused once the
// transaction has settled (#assertOpen). transaction.ts:924 names the method of the failing read, "submissionByRequest".
func TestSessionTransactionPlaceSubmissionIsATableWriteAndRefusedAfterSettling(t *testing.T) {
	harness := open()
	conversationId := createConversation(t, harness)
	requestId := "request-place"
	var submission durable.SubmissionRecord
	var entry durable.EntryRecord
	commit(t, harness.Session, func(tx durable.Tx) error {
		var err error
		if entry, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "input"}); err != nil {
			return err
		}
		submission, err = tx.CreateSubmission(durable.SubmissionCreate{ConversationId: conversationId, RequestId: &requestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued})
		return err
	})
	var settled durable.Tx
	commit(t, harness.Session, func(tx durable.Tx) error {
		settled = tx
		if err := tx.PlaceSubmission(submission.Id, entry.Id); err != nil {
			return err
		}
		_, err := tx.SubmissionByRequest(conversationId, requestId)
		var readAfterWrite *durable.ReadAfterWrite
		if !errors.As(err, &readAfterWrite) || readAfterWrite.Method != "submissionByRequest" || !strings.Contains(err.Error(), "Tx.submissionByRequest()") {
			t.Fatalf("a read after placeSubmission = %v, want ReadAfterWrite for submissionByRequest", err)
		}
		return nil
	})
	if err := settled.PlaceSubmission(submission.Id, entry.Id); err == nil {
		t.Fatal("PlaceSubmission on a settled transaction succeeded")
	}
}
