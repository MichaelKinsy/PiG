package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi types.ts:744-802 Tx: latestHeadMarker returns the newest visible entry that carries a head; submissionByRequest finds
// a committed submission by its conversation-scoped request ID; placeSubmission places a queued input at an entry in the
// same commit that appends the entry.
// Pi source: packages/durable/src/storage/memory.ts
// mutation-checked: zeroing the results of MemoryStorage.Submission fails it
func TestSessionTransactionLatestHeadMarkerSubmissionByRequestAndPlaceSubmission(t *testing.T) {
	harness := open()
	conversationId := createConversation(t, harness)
	other := createConversation(t, harness)

	commit(t, harness.Session, func(tx durable.Tx) error {
		marker, err := tx.LatestHeadMarker(conversationId)
		must(t, err)
		if marker != nil {
			t.Fatalf("a conversation without head entries has no marker: %+v", marker)
		}
		return nil
	})

	var headed durable.EntryRecord
	var submission durable.SubmissionRecord
	requestId := "request-a"
	commit(t, harness.Session, func(tx durable.Tx) error {
		var err error
		if _, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "note"}); err != nil {
			return err
		}
		if headed, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "summary", HeadSelf: true}); err != nil {
			return err
		}
		if _, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "later"}); err != nil {
			return err
		}
		submission, err = tx.CreateSubmission(durable.SubmissionCreate{ConversationId: conversationId, RequestId: &requestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued})
		return err
	})

	commit(t, harness.Session, func(tx durable.Tx) error {
		marker, err := tx.LatestHeadMarker(conversationId)
		must(t, err)
		if marker == nil || marker.Id != headed.Id || marker.Head == nil || *marker.Head != headed.Id {
			t.Fatalf("latest head marker = %+v, want entry %d", marker, headed.Id)
		}
		if marker, err = tx.LatestHeadMarker(other); err != nil || marker != nil {
			t.Fatalf("another conversation has no marker: %+v err=%v", marker, err)
		}
		found, err := tx.SubmissionByRequest(conversationId, requestId)
		must(t, err)
		expectEqual(t, found, &submission)
		if found, err = tx.SubmissionByRequest(other, requestId); err != nil || found != nil {
			t.Fatalf("a request ID is scoped to its conversation: %+v err=%v", found, err)
		}
		if found, err = tx.SubmissionByRequest(conversationId, "unknown"); err != nil || found != nil {
			t.Fatalf("unknown request: %+v err=%v", found, err)
		}
		return nil
	})

	var placedAt durable.EntryRecord
	commit(t, harness.Session, func(tx durable.Tx) error {
		var err error
		if placedAt, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "input"}); err != nil {
			return err
		}
		return tx.PlaceSubmission(submission.Id, placedAt.Id)
	})
	stored, err := harness.Storage.Submission(ctx, submission.Id)
	must(t, err)
	if stored == nil || stored.Status != durable.SubmissionPlaced || stored.Entry == nil || *stored.Entry != placedAt.Id {
		t.Fatalf("placed submission = %+v, want placed at %d", stored, placedAt.Id)
	}
}
