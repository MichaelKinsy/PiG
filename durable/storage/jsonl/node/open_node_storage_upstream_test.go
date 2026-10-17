package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// openNodeJsonlStorage (packages/durable/src/storage/jsonl/node.ts:5-12) opens JsonlStorage on the local file system with
// `new NodeExecutionEnv({ cwd: process.cwd() })`: a relative directory resolves against the working directory at open time, a missing
// directory is created, and what a commit wrote is there for the next open of the same directory.
// mutation-checked: resolving against a fixed working directory, opening a different directory, or a store that does not land in the open-time directory fails it
func TestOpenNodeJsonlStorageResolvesAgainstTheOpenTimeWorkingDirectoryAndPersists(t *testing.T) {
	opened := t.TempDir()
	elsewhere := t.TempDir()
	t.Chdir(opened)
	storage, err := OpenNodeJsonlStorage(t.Context(), "store", jsonl.JsonlStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// JsonlStorage.open resolves the directory to an absolute path against that working directory once, so a later chdir does not move it.
	t.Chdir(elsewhere)
	id, err := storage.MintId()
	if err != nil {
		t.Fatal(err)
	}
	taskID := durable.IdFromNumber[durable.TaskId](id)
	var checkpoint durable.JsonValue = map[string]any{"phase": "ready"}
	task := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
		Id: taskID, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.task", Version: 1,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
	}
	if _, err := storage.Commit(t.Context(), []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
		durable.TaskWrite{Value: task},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opened, "store", "main.jsonl")); err != nil {
		t.Fatalf("the open-time working directory has no store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "store")); err == nil {
		t.Fatal("the store followed the later working directory")
	}

	t.Chdir(opened)
	reopened, err := OpenNodeJsonlStorage(t.Context(), "store", jsonl.JsonlStorageOptions{Fsync: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close(t.Context()) }()
	got, err := reopened.Task(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Kind != "test.task" || got.State.Status != durable.TaskPending || got.State.Checkpoint == nil {
		t.Fatalf("reopened task = %+v, want the committed pending task", got)
	}
	if conversation, err := reopened.Conversation(t.Context(), durable.ROOT_CONVERSATION_ID); err != nil || conversation == nil {
		t.Fatalf("reopened conversation = %+v, %v", conversation, err)
	}
	if next, err := reopened.MintId(); err != nil || next <= id {
		t.Fatalf("next id = %d, %v; want one after %d", next, err, id)
	}
}
