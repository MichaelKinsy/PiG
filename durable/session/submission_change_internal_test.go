package session

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// applySubmissionChange (transaction.ts:67-82): a settled record (done, unanswered) stays; placing a queued submission turns an input placed and a
// write done and records its entry; placing anything not queued fails "Submission N is not queued"; only a placed input can be answered (done)
// ("Submission N is not a placed input"); an unanswered settlement keeps the record's entry and adds the reason and detail, a done one the answer.
// Pi: packages/durable/src/session/transaction.ts:67-82
func TestApplySubmissionChangeFollowsPisStateMachine(t *testing.T) {
	record := func(kind durable.SubmissionType, status durable.SubmissionStatus) durable.SubmissionRecord {
		entry := durable.EntryId(7)
		r := durable.SubmissionRecord{Id: 3, ConversationId: 1, Type: kind, Status: status}
		if status == durable.SubmissionPlaced || status == durable.SubmissionDone || status == durable.SubmissionUnanswered {
			r.Entry = &entry
		}
		return r
	}
	placeAt := submissionChange{placed: true, entry: 9}
	answer := submissionChange{settlement: durable.SubmissionSettlement{Status: durable.SubmissionDone, Answer: 11}}
	giveUp := submissionChange{settlement: durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "cancelled", Detail: "d"}}

	for _, tc := range []struct {
		name    string
		current durable.SubmissionRecord
		change  submissionChange
		changed bool
		wantErr string
		status  durable.SubmissionStatus
		entry   *durable.EntryId
		answer  *durable.EntryId
		reason  string
	}{
		{name: "done stays against a placement", current: record(durable.SubmissionTypeWrite, durable.SubmissionDone), change: placeAt, status: durable.SubmissionDone, entry: new(durable.EntryId(7))},
		{name: "unanswered stays against an answer", current: record(durable.SubmissionTypeInput, durable.SubmissionUnanswered), change: answer, status: durable.SubmissionUnanswered, entry: new(durable.EntryId(7))},
		{name: "a queued input is placed", current: record(durable.SubmissionTypeInput, durable.SubmissionQueued), change: placeAt, changed: true, status: durable.SubmissionPlaced, entry: new(durable.EntryId(9))},
		{name: "a queued write is done", current: record(durable.SubmissionTypeWrite, durable.SubmissionQueued), change: placeAt, changed: true, status: durable.SubmissionDone, entry: new(durable.EntryId(9))},
		{name: "a placed record cannot be placed again", current: record(durable.SubmissionTypeInput, durable.SubmissionPlaced), change: placeAt, wantErr: "Submission 3 is not queued"},
		{name: "a placed input is answered", current: record(durable.SubmissionTypeInput, durable.SubmissionPlaced), change: answer, changed: true, status: durable.SubmissionDone, entry: new(durable.EntryId(7)), answer: new(durable.EntryId(11))},
		{name: "a queued input cannot be answered", current: record(durable.SubmissionTypeInput, durable.SubmissionQueued), change: answer, wantErr: "Submission 3 is not a placed input"},
		{name: "a queued input can be left unanswered", current: record(durable.SubmissionTypeInput, durable.SubmissionQueued), change: giveUp, changed: true, status: durable.SubmissionUnanswered, reason: "cancelled"},
		{name: "a placed input keeps its entry when left unanswered", current: record(durable.SubmissionTypeInput, durable.SubmissionPlaced), change: giveUp, changed: true, status: durable.SubmissionUnanswered, entry: new(durable.EntryId(7)), reason: "cancelled"},
	} {
		got, changed, err := applySubmissionChange(tc.current, tc.change)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr || changed {
				t.Errorf("%s: err %v changed %v, want error %q", tc.name, err, changed, tc.wantErr)
			}
			continue
		}
		if err != nil || changed != tc.changed || got.Status != tc.status {
			t.Errorf("%s: status %q changed %v err %v, want %q %v", tc.name, got.Status, changed, err, tc.status, tc.changed)
			continue
		}
		same := func(a, b *durable.EntryId) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
		if !same(got.Entry, tc.entry) || !same(got.Answer, tc.answer) {
			t.Errorf("%s: entry %v answer %v, want %v %v", tc.name, got.Entry, got.Answer, tc.entry, tc.answer)
		}
		if tc.reason != "" && (got.Reason == nil || *got.Reason != tc.reason || got.Detail != "d") {
			t.Errorf("%s: reason %v detail %v, want %q and d", tc.name, got.Reason, got.Detail, tc.reason)
		}
		if tc.reason == "" && (got.Reason != nil || got.Detail != nil) {
			t.Errorf("%s: a record without a settlement reason carries reason %v detail %v", tc.name, got.Reason, got.Detail)
		}
	}
}
