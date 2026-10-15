package jsonl_test

import (
	"errors"
	"strings"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// TestJsonlPublicationFailuresNameTheFailedStep ports storage.ts:292, :297 and :301: a failure while publishing a commit poisons the storage with
// a cause `JSONL <action> failed: <file error>`, where the action is `append to <sidecar file>` for a sidecar record, `flush of <sidecar file>` for
// its fsync (only with fsync on) and `append to main.jsonl` for the main marker.
func TestJsonlPublicationFailuresNameTheFailedStep(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options jsonl.JsonlStorageOptions
		fail    failure
		sidecar bool
		want    string
	}{
		{"sidecar append", jsonl.JsonlStorageOptions{}, failure{operation: "append", call: 1, mode: "before"}, true, "JSONL append to doc-2.jsonl failed: injected append failure"},
		{"sidecar flush", jsonl.JsonlStorageOptions{Fsync: true}, failure{operation: "flush", call: 1, mode: "before"}, true, "JSONL flush of doc-2.jsonl failed: injected flush failure"},
		{"main append", jsonl.JsonlStorageOptions{}, failure{operation: "append", call: 1, mode: "before"}, false, "JSONL append to main.jsonl failed: injected append failure"},
	} {
		directory := tempDirectory(t)
		instrumented := newInstrumentedEnv(directory)
		storage := openStorage(t, directory, instrumented, tc.options)
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		entry := mint[durable.EntryId](t, storage)
		instrumented.fail(tc.fail)
		var writes []durable.StorageWrite
		if tc.sidecar {
			writes = []durable.StorageWrite{durable.DocumentCreateWrite{Record: sessionDocument(id, "first"), Content: base(chorddelta.JsonObjectOf("text", "α"))}}
		} else {
			writes = []durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{Id: entry, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "later"}}}
		}
		_, err := storage.Commit(testContext, writes)
		poisoned, ok := errors.AsType[*jsonl.JsonlStoragePoisonedError](err)
		if !ok || poisoned.Cause == nil {
			t.Fatalf("%s: commit error = %v (%T), want a poisoned error with a cause", tc.name, err, err)
		}
		if got := poisoned.Cause.Error(); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: cause %q, want prefix %q", tc.name, got, tc.want)
		}
	}
}
