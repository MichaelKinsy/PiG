package pico3

import (
	"context"
	"testing"
)

func TestMemoryStoragePatchOwnsOutcomePayload(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()
	_, err := storage.Commit(ctx, []Write{{Type: WriteTask, Task: &Task{Id: 1, ConversationId: 2, Kind: "test", Status: TaskPending, After: []Id{}, Owns: []Id{}}}})
	if err != nil {
		t.Fatal(err)
	}
	result := JsonObject{"value": "original"}
	_, err = storage.Commit(ctx, []Write{{Type: WriteTaskPatch, Patch: &TaskPatch{Id: 1, Status: new(TaskTerminal), Outcome: &Outcome{Status: OutcomeCompleted, Result: result}}}})
	if err != nil {
		t.Fatal(err)
	}
	result["value"] = "mutated"
	task, err := storage.Task(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if task.Outcome.Result.(map[string]any)["value"] != "original" {
		t.Fatal("caller changed persisted outcome")
	}
}

func TestMemoryStorageMalformedDirectDocumentMatchesUpstreamPartialFailure(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()
	_, err := storage.Commit(ctx, []Write{{Type: WriteConversation, Conversation: &Conversation{Id: 1}}, {Type: WriteDoc, Ref: SessionDoc(), Ops: []Op{{"s", []any{"missing", "value"}, true}}}})
	if err == nil {
		t.Fatal("invalid document operation silently accepted")
	}
	conversation, err := storage.Conversation(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	// The upstream direct Storage API applies earlier table writes before a
	// malformed session-document delta throws. The failed batch leaves the
	// sequence and ID high-water unchanged; Session-generated deltas are valid.
	if conversation == nil {
		t.Fatal("upstream partial conversation effect was lost")
	}
	if storage.MintId() != 1 {
		t.Fatal("failed batch advanced ID")
	}
	seq, err := storage.Commit(ctx, nil)
	if err != nil || seq != 1 {
		t.Fatalf("next sequence=%d error=%v", seq, err)
	}
}

func TestJsonlRecordRejectsNullCounters(t *testing.T) {
	for _, line := range []string{`{"seq":null,"maxId":1,"writes":[]}`, `{"seq":1,"maxId":null,"writes":[]}`} {
		if _, err := parseRecord("session.jsonl", []byte(line)); err == nil {
			t.Fatalf("accepted %s", line)
		}
	}
}
