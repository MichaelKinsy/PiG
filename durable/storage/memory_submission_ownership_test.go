package storage_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi memory.ts:569-573 (submission) and :595-603 (submissionByRequest) return clone(record), and the commit retains clone(write) (:272): a value passed to commit and a
// submission a read returned are both the caller's to edit without reaching what the storage retains. The by-ID and by-request reads share the retained record.
// mutation-checked: returning the retained record without cloneSubmission fails it, from SubmissionByRequest (1 subtest) and from Submission (both, as the by-request read sees the edit).
func TestMemorySubmissionReadsAreCopies(t *testing.T) {
	ctx := t.Context()
	memory := storage.NewMemoryStorage()
	submissionID := durable.IdFromNumber[durable.SubmissionId](2)
	requestID, reason := "req-1", "gone"
	record := durable.SubmissionRecord{
		Id: submissionID, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &requestID, Type: durable.SubmissionTypeWrite,
		Status: durable.SubmissionUnanswered, Reason: &reason, Detail: map[string]any{"nested": []any{1}},
	}
	if _, err := memory.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
		durable.SubmissionWrite{Value: record},
	}); err != nil {
		t.Fatal(err)
	}
	reason = "edited after commit"
	record.Detail.(map[string]any)["nested"].([]any)[0] = 99
	wantDetail := map[string]any{"nested": []any{1}}

	reads := map[string]func() (*durable.SubmissionRecord, error){
		"Submission": func() (*durable.SubmissionRecord, error) { return memory.Submission(ctx, submissionID) },
		"SubmissionByRequest": func() (*durable.SubmissionRecord, error) {
			return memory.SubmissionByRequest(ctx, durable.ROOT_CONVERSATION_ID, "req-1")
		},
	}
	for _, name := range []string{"Submission", "SubmissionByRequest"} {
		t.Run(name, func(t *testing.T) {
			first, err := reads[name]()
			if err != nil || first == nil {
				t.Fatalf("read = %+v, %v", first, err)
			}
			if first.Reason == nil || *first.Reason != "gone" || !reflect.DeepEqual(first.Detail, wantDetail) {
				t.Fatalf("retained record %+v, want reason %q and detail %#v (the commit input was edited after commit)", first, "gone", wantDetail)
			}
			*first.Reason = "edited through a read"
			*first.RequestId = "edited"
			first.Detail.(map[string]any)["nested"].([]any)[0] = 77
			second, err := reads[name]()
			if err != nil || second == nil {
				t.Fatalf("second read = %+v, %v", second, err)
			}
			if *second.Reason != "gone" || *second.RequestId != "req-1" || !reflect.DeepEqual(second.Detail, wantDetail) {
				t.Fatalf("second read %+v (reason %q, request %q), want the retained record unchanged", second, *second.Reason, *second.RequestId)
			}
		})
	}
}
