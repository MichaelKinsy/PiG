package jsonl_test

import (
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// Pi storage.ts:842-843 assertUsable checks closed before the poison: a poisoned storage that is then closed answers "JsonlStorage is closed", not
// its JsonlStoragePoisonedError. close (:377-380) does not assert usability, so closing a poisoned storage succeeds.
func TestJsonlClosedErrorWinsOverThePoison(t *testing.T) {
	directory := tempDirectory(t)
	instrumented := newInstrumentedEnv(directory)
	storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
	createRoot(t, storage)
	id := mint[durable.DocumentId](t, storage)
	instrumented.fail(failure{operation: "append", call: 1, mode: "before"})
	if _, err := storage.Commit(testContext, []durable.StorageWrite{
		durable.DocumentCreateWrite{Record: sessionDocument(id, "first"), Content: base(chorddelta.JsonObjectOf("text", "a"))},
	}, func(durable.Seq) {}); err == nil {
		t.Fatal("the failing commit succeeded, so the storage was not poisoned")
	}
	if err := storage.Close(testContext); err != nil {
		t.Fatalf("Close of a poisoned storage = %v, want nil", err)
	}
	if _, err := storage.MintId(); err == nil || err.Error() != "JsonlStorage is closed" {
		t.Fatalf("MintId after poison then Close = %v, want the closed error", err)
	}
}
