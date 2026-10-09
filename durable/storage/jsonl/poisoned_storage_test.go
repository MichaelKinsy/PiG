package jsonl_test

import (
	"errors"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// Pi storage.ts:88-93 and 836-844: a publication failure poisons the storage with one JsonlStoragePoisonedError that carries the failure as its
// cause; every later call (reads, mints and commits) throws that same error object (`poisonError ??= ...`, assertUsable), and a reopen over the
// same directory is usable. jsonl-storage.test.ts:458-483 only checks the message text; this pins the error's type, identity and cause.
func TestJsonlStoragePoisonsWithOneCausedErrorForEveryLaterCall(t *testing.T) {
	directory := tempDirectory(t)
	instrumented := newInstrumentedEnv(directory)
	storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
	createRoot(t, storage)
	firstId := mint[durable.DocumentId](t, storage)
	laterId := mint[durable.EntryId](t, storage)
	instrumented.fail(failure{operation: "append", call: 1, mode: "before"})
	_, commitErr := storage.Commit(testContext, []durable.StorageWrite{
		durable.DocumentCreateWrite{Record: sessionDocument(firstId, "first"), Content: base(chorddelta.JsonObjectOf("text", "α"))},
	})
	poisoned, ok := errors.AsType[*jsonl.JsonlStoragePoisonedError](commitErr)
	if !ok {
		t.Fatalf("commit error = %v (%T), want *JsonlStoragePoisonedError", commitErr, commitErr)
	}
	if poisoned.Cause == nil || errors.Unwrap(poisoned) != poisoned.Cause { //nolint:errorlint // the check is identity: one caused error instance, not any wrapped match
		t.Fatalf("the poisoned error has cause %v, want the publication failure", poisoned.Cause)
	}

	_, readErr := storage.Document(testContext, firstId, durable.CurrentPoint)
	_, mintErr := storage.MintId()
	_, laterCommitErr := storage.Commit(testContext, []durable.StorageWrite{
		durable.EntryWrite{Value: durable.EntryRecord{Id: laterId, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "later"}},
	})
	for name, err := range map[string]error{"read": readErr, "mint": mintErr, "commit": laterCommitErr} {
		if err != error(poisoned) { //nolint:errorlint // the check is identity: every later call returns the same error instance
			t.Errorf("%s after poisoning returned %v (%T), want the same *JsonlStoragePoisonedError as the failed commit", name, err, err)
		}
	}

	reopened := open(t, directory)
	if document := must(reopened.Document(testContext, firstId, durable.CurrentPoint)); document != nil {
		t.Fatalf("the unconfirmed publication survived the reopen: %+v", document)
	}
	commit(t, reopened, durable.EntryWrite{Value: durable.EntryRecord{Id: mint[durable.EntryId](t, reopened), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "after"}})
}
