package pico3

import (
	"context"
	"errors"
	"testing"
)

func TestTransactionDirectReadsObserveCreations(t *testing.T) {
	env := openEnv(t, openOptions{})
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		inputID, err := tx.Write(1, NewEntry{Kind: "same-batch", Data: JsonObject{"value": 1}})
		if err != nil {
			return nil, err
		}
		input := must(tx.Input(inputID))
		equal(t, input.Status, InputDone, "input")
		entry := must(tx.Entry(*input.Entry))
		equal(t, entry.Data, JsonObject{"value": 1}, "entry")
		entries := must(tx.Entries([]Id{entry.Id}))
		equal(t, entries[entry.Id].Id, entry.Id, "batch entries")
		ref := must(tx.CreateTask(Kinds.Plugin, JsonObject{"handler": "missing", "input": nil}, TaskOptions{ConversationId: new(Id(1)), Background: true}))
		equal(t, must(tx.Task(ref.Id)).Status, TaskPending, "task")
		child := must(tx.CreateConversation(ConversationSpec{Rewindable: JsonObject{"profile": "child"}, Sticky: JsonObject{"followUpMode": "all"}}))
		equal(t, must(tx.Conversation(child)).Id, child, "conversation")
		equal(t, must(tx.Snapshot(RewindableDoc(child)))["profile"], "child", "rewindable")
		equal(t, must(tx.Snapshot(StickyDoc(child)))["followUpMode"], "all", "sticky")
		return nil, nil
	})
	check(t, err)
}

func TestCaughtReadAfterWriteRollsBackDocumentsAndTables(t *testing.T) {
	env := openEnv(t, openOptions{})
	namespace := must(env.h.Namespace("poison", NamespaceDefaults{Sticky: JsonObject{"value": false}, Rewindable: JsonObject{"alsoPoisoned": false}}, nil))
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		node := must(tx.Plugins(namespace))
		node.Set("value", true)
		if _, err := tx.Write(1, NewEntry{Kind: "rollback"}); err != nil {
			return nil, err
		}
		_, scanErr := tx.ScanEntries(EntryScan{ConversationId: 1, Limit: 10})
		if _, ok := errors.AsType[*ReadAfterWrite](scanErr); !ok {
			t.Fatalf("caught scan failure: %v", scanErr)
		}
		node.Set("alsoPoisoned", true)
		return nil, nil
	})
	if _, ok := errors.AsType[*ReadAfterWrite](err); !ok {
		t.Fatalf("poison: %v", err)
	}
	equal(t, len(env.entries()), 0, "rolled back entries")
	if _, ok := obj(env.sticky(), "plugins")["poison"]; ok {
		t.Fatal("rolled back plugin state published")
	}
	if _, ok := obj(must(env.root.Rewindable(bg)), "plugins")["poison"]; ok {
		t.Fatal("rolled back rewindable state published")
	}
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		node := must(tx.Plugins(namespace))
		node.Set("value", true)
		return nil, nil
	})
	check(t, err)
	equal(t, obj(obj(env.sticky(), "plugins"), "poison")["value"], true, "clean next transaction")
}

func TestHostCoreOperationsReject(t *testing.T) {
	env := openEnv(t, openOptions{})
	for _, operation := range []func(*Tx) error{
		func(tx *Tx) error { _, err := tx.AppendEntry(1, NewEntry{Kind: "forged"}); return err },
		func(tx *Tx) error {
			_, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", ConversationId: new(Id(1)), Input: JsonObject{}})
			return err
		},
		func(tx *Tx) error { _, err := tx.Send(1, SendInput{Content: "forged"}); return err },
		func(tx *Tx) error { return tx.MarkTask(999) },
	} {
		_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { return nil, operation(tx) })
		if _, ok := errors.AsType[*Forbidden](err); !ok {
			t.Fatalf("core authority: %v", err)
		}
	}
}
