package jsonl_test

import (
	"context"
	"errors"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// pi: packages/durable/src/storage/jsonl/storage.ts planReclamations (:450-490), adoptSidecarState (:494-503), resolveFile (:825-828).

// A document that is current-only (a session-scoped one) and is retired has its sidecar removed, including when the same commit creates it
// (:474 createdCurrentOnlyDocuments), and a base change in the retiring commit does not rewrite it (:481 `|| retiredDocuments.has(id)`).
func TestJsonlRetiredCurrentOnlyDocumentSidecarIsRemovedByTheCommitThatRetiresIt(t *testing.T) {
	t.Run("created and retired in one commit", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		commit(t, storage,
			durable.DocumentCreateWrite{Record: sessionDocument(id, "first"), Content: base(chorddelta.JsonObjectOf("count", 1))},
			durable.DocumentRetireWrite{Id: id},
		)
		if fileExists(t, file(directory, "doc", int64(id))) {
			t.Fatal("the sidecar of a document created and retired in one commit remains")
		}
	})
	t.Run("a base change in the retiring commit does not rewrite the sidecar", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(id, "first"), Content: base(chorddelta.JsonObjectOf("count", 1))})
		commit(t, storage,
			durable.DocumentChangeWrite{Id: id, Content: base(chorddelta.JsonObjectOf("count", 2))},
			durable.DocumentRetireWrite{Id: id},
		)
		if fileExists(t, file(directory, "doc", int64(id))) {
			t.Fatal("the sidecar of a retired document was rewritten by its base change")
		}
	})
}

// adoptSidecarState drops a task from the live set once a terminal record replaces its sidecar (:500), so a later terminal write for the
// same task has no sidecar to reclaim and removes nothing.
func TestJsonlTerminalTaskSidecarIsReclaimedOnlyOnce(t *testing.T) {
	directory := tempDirectory(t)
	instrumented := newInstrumentedEnv(directory)
	storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
	createRoot(t, storage)
	taskId := mint[durable.TaskId](t, storage)
	commit(t, storage, durable.TaskWrite{Value: pendingTask(taskId, "ready")})
	instrumented.resetObservations()
	commit(t, storage, durable.TaskWrite{Value: terminalTask(taskId)})
	if got := instrumented.calls["remove"]; got != 1 {
		t.Fatalf("terminal write removed %d sidecars, want 1 (%v)", got, instrumented.operations)
	}
	instrumented.resetObservations()
	commit(t, storage, durable.TaskWrite{Value: terminalTask(taskId)})
	if got := instrumented.calls["remove"]; got != 0 {
		t.Fatalf("a second terminal write removed %d sidecars, want 0 (%v)", got, instrumented.operations)
	}
}

// joinFailingFileSystem fails every JoinPath once armed.
type joinFailingFileSystem struct {
	*instrumentedEnv
	armed bool
}

func (fileSystem *joinFailingFileSystem) JoinPath(ctx context.Context, parts []string) (string, error) {
	if fileSystem.armed {
		return "", injected("injected join failure", "")
	}
	return fileSystem.instrumentedEnv.JoinPath(ctx, parts)
}

// resolveFile names the failed step: `JSONL path join failed: <file error>` (storage.ts append, :346). The batch is applied in memory by
// then, so Pi at 6d4b7443 poisons the storage (commit(): any failure once a batch applied), with that failure as the
// cause; a reopened storage has no record of the batch.
func TestJsonlCommitNamesAFailedSidecarPathJoin(t *testing.T) {
	directory := tempDirectory(t)
	fileSystem := &joinFailingFileSystem{instrumentedEnv: newInstrumentedEnv(directory)}
	storage := openStorage(t, directory, fileSystem, jsonl.JsonlStorageOptions{})
	createRoot(t, storage)
	id := mint[durable.DocumentId](t, storage)
	fileSystem.armed = true
	_, err := durabletest.CommitWrites(testContext, storage, []durable.StorageWrite{durable.DocumentCreateWrite{Record: sessionDocument(id, "first"), Content: base(chorddelta.JsonObjectOf("count", 1))}})
	poisoned, isPoisoned := errors.AsType[*jsonl.JsonlStoragePoisonedError](err)
	if !isPoisoned {
		t.Fatalf("commit error = %v, want the poisoned error", err)
	}
	if poisoned.Cause == nil || poisoned.Cause.Error() != "JSONL path join failed: injected join failure" {
		t.Fatalf("poison cause = %v, want JSONL path join failed: injected join failure", poisoned.Cause)
	}
	fileSystem.armed = false
	reopened := open(t, directory)
	if found := must(reopened.Document(testContext, id, durable.CurrentPoint)); found != nil {
		t.Fatal("a document whose sidecar path failed to resolve was recovered")
	}
}
