package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// pi: packages/durable/src/storage/memory.ts (apply indexes taskIdsByStatus and submissionIdsByStatus, which hold exactly the closed status sets)
// An unknown status cannot be indexed in Pi, so a commit carrying one fails and a JSONL recovery reports the commit as invalid; the Go indexes are maps that would take any key.
func TestMemoryStorageRejectsAnUnknownTaskOrSubmissionStatus(t *testing.T) {
	task := durable.TaskWrite{Value: durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: 2, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "k", Version: 1, State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: "bogus"}}}
	submission := durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: 3, ConversationId: durable.ROOT_CONVERSATION_ID, Type: durable.SubmissionTypeWrite, Status: "bogus"}}
	for name, write := range map[string]durable.StorageWrite{"task": task, "submission": submission} {
		memory := storage.NewMemoryStorage()
		if _, err := memory.Commit(context.Background(), []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}); err != nil {
			t.Fatal(err)
		}
		_, err := memory.Commit(context.Background(), []durable.StorageWrite{write})
		if err == nil || !strings.Contains(err.Error(), "Unknown "+name+" status") {
			t.Errorf("%s with an unknown status: %v", name, err)
		}
	}
}
