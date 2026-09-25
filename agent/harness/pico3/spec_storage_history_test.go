package pico3

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Source: packages/agent/test/harness/pico3/spec-storage-history.test.ts.
func TestStorageRejectsDuplicateGlobalIDBeforePublishing(t *testing.T) {
	storage := NewMemoryStorage()
	defer func() { check(t, storage.Close(bg)) }()
	_, err := storage.Commit(bg, []Write{{Type: WriteConversation, Conversation: &Conversation{Id: 40}}, {Type: WriteDoc, Ref: SessionDoc(), Ops: []Op{{"r", JsonObject{"plugins": JsonObject{"leaked": JsonObject{"value": true}}}}}}, {Type: WriteEntry, Entry: &Entry{Id: 40, ConversationId: 40, Kind: "duplicate-global-id"}}})
	if err == nil || !strings.Contains(err.Error(), "created twice") {
		t.Fatalf("duplicate failure: %v", err)
	}
	equal(t, must(storage.Conversation(bg, 40)), (*Conversation)(nil), "conversation unpublished")
	equal(t, len(must(storage.Entries(bg, []Id{40}))), 0, "entry unpublished")
	equal(t, must(storage.Doc(bg, SessionDoc())), JsonObject{"plugins": JsonObject{}}, "document unpublished")
	equal(t, must(storage.Commit(bg, []Write{{Type: WriteConversation, Conversation: &Conversation{Id: 1}}})), Seq(1), "sequence not advanced")
	equal(t, storage.MintId(), Id(2), "failed id does not advance highwater")
	equal(t, must(storage.Conversation(bg, 1)).Id, Id(1), "subsequent commit works")
}

func TestStorageReadsIsolateEveryMutableFamily(t *testing.T) {
	storage := NewMemoryStorage()
	defer func() { check(t, storage.Close(bg)) }()
	must(storage.Commit(bg, []Write{
		{Type: WriteConversation, Conversation: &Conversation{Id: 1, Sections: []SectionSeed{{Key: "x", Value: JsonObject{"nested": 1}}}}},
		{Type: WriteEntry, Entry: &Entry{Id: 2, ConversationId: 1, Kind: "note", Data: JsonObject{"nested": JsonObject{"value": 1}}}},
		{Type: WriteTask, Task: &Task{Id: 3, ConversationId: 1, Kind: "task", Input: JsonObject{"nested": 1}, Status: TaskPending, After: []Id{}, Owns: []Id{}}},
		{Type: WriteInput, Input: &Input{Id: 4, ConversationId: 1, Status: InputQueued, RequestId: "r"}},
		{Type: WriteDoc, Ref: RewindableDoc(1), Ops: []Op{{"r", JsonObject{"plugins": JsonObject{"p": JsonObject{"nested": JsonObject{"value": 1}}}}}}},
	}))
	conversation := must(storage.Conversation(bg, 1))
	entry := must(storage.Entries(bg, []Id{2}))[2]
	task := must(storage.Task(bg, 3))
	input := must(storage.InputByRequest(bg, 1, "r"))
	document := must(storage.Doc(bg, RewindableDoc(1)))
	conversation.Sections[0].Value = "mutated"
	asObject(entry.Data["nested"])["value"] = 9
	asObject(task.Input)["nested"] = 9
	input.Status = InputDone
	asObject(asObject(asObject(document["plugins"])["p"])["nested"])["value"] = 9
	equal(t, string(mustJSON(must(storage.Conversation(bg, 1)).Sections)), string(mustJSON([]SectionSeed{{Key: "x", Value: JsonObject{"nested": 1}}})), "sections snapshot")
	equal(t, must(storage.Entries(bg, []Id{2}))[2].Data, JsonObject{"nested": JsonObject{"value": 1}}, "entry snapshot")
	equal(t, must(storage.Task(bg, 3)).Input, JsonObject{"nested": 1}, "task snapshot")
	equal(t, must(storage.InputByRequest(bg, 1, "r")).Status, InputQueued, "input snapshot")
	equal(t, must(storage.Doc(bg, RewindableDoc(1))), JsonObject{"plugins": JsonObject{"p": JsonObject{"nested": JsonObject{"value": 1}}}}, "document snapshot")
}

type historyRejectingStorage struct {
	*MemoryStorage
	reject bool
}

func (s *historyRejectingStorage) Commit(ctx context.Context, writes []Write) (Seq, error) {
	if s.reject {
		return 0, errors.New("publication cut")
	}
	return s.MemoryStorage.Commit(ctx, writes)
}

func TestStorageRejectedPersistenceFaultsWithoutPublishingCache(t *testing.T) {
	storage := &historyRejectingStorage{MemoryStorage: NewMemoryStorage()}
	h := must(OpenHarness(bg, storage, HarnessOptions{Models: newFake(fakeOptions{respond: echoScript})}))
	defer func() { _ = h.Close(bg) }()
	state := must(h.Namespace("test.rejected-cut", NamespaceDefaults{Rewindable: JsonObject{"cut": JsonObject{"shouldNotPersist": false}}}, nil))
	check(t, h.Resume())
	root := must(h.Root(bg))
	storage.reject = true
	_, err := root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		node, err := tx.Plugins(state)
		if err != nil {
			return nil, err
		}
		node.Set("cut", JsonObject{"shouldNotPersist": true})
		return nil, nil
	})
	if _, ok := errors.AsType[*Faulted](err); !ok {
		t.Fatalf("commit: %v", err)
	}
	stored := must(storage.Doc(bg, RewindableDoc(root.Id)))
	if _, ok := asObject(stored["plugins"])["test.rejected-cut"]; ok {
		t.Fatal("failed document published")
	}
	_, err = root.Write(bg, NewEntry{Kind: "later"})
	if _, ok := errors.AsType[*Faulted](err); !ok {
		t.Fatalf("later write: %v", err)
	}
}

func TestStorageJSONLPartialSidecarAppendIsUnpublished(t *testing.T) {
	kind := quickKind(t, "never", func(Task) JsonValue { return nil })
	env := openEnv(t, openOptions{backend: "jsonl", taskKinds: []*Kind{kind}})
	must(env.h.Hold())
	ref := createTestTask(t, env, kind, nil)
	env.crash()
	storage := must(OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)}))
	appendRecord := storage.appendRecord
	appends := 0
	storage.appendRecord = func(file *os.File, record jsonlRecord) error {
		appends++
		if appends == 2 {
			return errors.New("injected sidecar append failure")
		}
		return appendRecord(file, record)
	}
	_, err := storage.Commit(bg, []Write{{Type: WriteTaskPatch, Patch: &TaskPatch{Id: ref.Id, Status: new(TaskRunning), Checkpoint: Checkpoint{"phase": "started"}}}, {Type: WriteDoc, Ref: StickyDoc(1), Ops: []Op{{"s", []any{"followUpMode"}, "all"}}}})
	if err == nil || !strings.Contains(err.Error(), "injected sidecar append failure") {
		t.Fatalf("append: %v", err)
	}
	check(t, storage.Close(bg))
	next := must(OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)}))
	defer func() { check(t, next.Close(bg)) }()
	task := must(next.Task(bg, ref.Id))
	equal(t, task.Status, TaskPending, "task tail discarded")
	equal(t, task.Checkpoint, Checkpoint(nil), "checkpoint unpublished")
	equal(t, must(next.Doc(bg, StickyDoc(1)))["followUpMode"], "one-at-a-time", "sticky unchanged")
}

func TestStorageUnconfirmedTailCannotAdvanceHighwater(t *testing.T) {
	env := openEnv(t, openOptions{backend: "jsonl"})
	must(env.root.Write(bg, NewEntry{Kind: "confirmed"}))
	env.crash()
	lines := strings.Split(strings.TrimSpace(string(must(os.ReadFile(filepath.Join(env.dir, "main.jsonl"))))), "\n")
	var main jsonlRecord
	check(t, json.Unmarshal([]byte(lines[len(lines)-1]), &main))
	record := jsonlRecord{Seq: main.Seq + 1, MaxId: 999999, Writes: []Write{{Type: WriteDoc, Ref: StickyDoc(999), Ops: []Op{{"r", JsonObject{"inbox": []any{}, "turn": JsonObject{"tools": []any{}}, "tasks": JsonObject{}, "plugins": JsonObject{}}}}}}}
	check(t, os.WriteFile(filepath.Join(env.dir, "sticky-999.jsonl"), append(mustJSON(record), '\n'), 0600))
	storage := must(OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)}))
	defer func() { check(t, storage.Close(bg)) }()
	if storage.MintId() >= 999999 {
		t.Fatal("unpublished highwater leaked")
	}
	equal(t, must(storage.Doc(bg, StickyDoc(999))), JsonObject(nil), "unconfirmed document ignored")
}

func TestStorageForkUsesFinalSameCommitState(t *testing.T) {
	env := openEnv(t, openOptions{})
	state := must(env.h.Namespace("test.fork-history", NamespaceDefaults{Rewindable: JsonObject{"version": ""}}, nil))
	id := must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (Id, error) {
		input, err := tx.Write(1, NewEntry{Kind: "anchor"})
		if err != nil {
			return 0, err
		}
		node, err := tx.Plugins(state)
		if err != nil {
			return 0, err
		}
		node.Set("version", "same-commit-final")
		return input, nil
	}))
	record := env.input(id)
	fork := must(env.root.Fork(bg, record.Entry, ConversationSpec{}))
	equal(t, asObject(asObject(must(fork.Rewindable(bg))["plugins"])["test.fork-history"])["version"], "same-commit-final", "commit granular fork")
}

func historyConversationWrites(conversation Conversation) []Write {
	return []Write{{Type: WriteConversation, Conversation: &conversation}, {Type: WriteDoc, Ref: RewindableDoc(conversation.Id), Ops: []Op{{"r", JsonObject{"thinkingLevel": "off", "selectedTools": []any{}, "profile": "default", "threshold": 0, "keepRecent": 20000, "plugins": JsonObject{}}}}}, {Type: WriteDoc, Ref: StickyDoc(conversation.Id), Ops: []Op{{"r", JsonObject{"retry": JsonObject{"enabled": false, "maxRetries": 0, "baseDelayMs": 1}, "steeringMode": "all", "followUpMode": "all", "inbox": []any{}, "turn": JsonObject{"tools": []any{}}, "tasks": JsonObject{}, "plugins": JsonObject{}}}}}}
}
func historyHarness(t *testing.T, storage Storage) *Harness {
	t.Helper()
	h := must(OpenHarness(bg, storage, HarnessOptions{Models: newFake(fakeOptions{respond: echoScript})}))
	t.Cleanup(func() { check(t, h.Close(bg)) })
	check(t, h.Resume())
	return h
}
func historyViewIDs(view JsonObject) []Id {
	var ids []Id
	for _, entry := range arr(view, "entries") {
		ids = append(ids, Id(numberOr(asObject(entry)["id"], 0)))
	}
	return ids
}

func TestStorageActiveTranscriptPreservesInheritedHeadAndEdits(t *testing.T) {
	storage := NewMemoryStorage()
	writes := historyConversationWrites(Conversation{Id: 1})
	for _, entry := range []Entry{{Id: 2, ConversationId: 1, Kind: "pi.user", Model: []JsonObject{{"role": "user", "content": "base", "timestamp": 1}}}, {Id: 3, ConversationId: 1, Kind: "pi.summary", Head: new(Id(2))}, {Id: 4, ConversationId: 1, Kind: "pi.assistant", Data: JsonObject{"reason": "aborted"}}, {Id: 5, ConversationId: 1, Kind: "plugin.note", Edits: []ContextEdit{{Target: 2, Action: "omit"}}}} {
		writes = append(writes, Write{Type: WriteEntry, Entry: &entry})
	}
	writes = append(writes, historyConversationWrites(Conversation{Id: 10, Parent: &ConversationParent{ConversationId: 1, At: 5}})...)
	writes = append(writes, Write{Type: WriteEntry, Entry: &Entry{Id: 11, ConversationId: 10, Kind: "pi.handoff", Head: new(Id(2))}})
	must(storage.Commit(bg, writes))
	h := historyHarness(t, storage)
	child := must(h.Conversation(bg, 10))
	collector := collectWatch(t, child)
	equal(t, historyViewIDs(collector.view), []Id{2, 3, 4, 5, 11}, "active rendering range")
	entries := arr(collector.view, "entries")
	equal(t, asObject(entries[3])["edits"], []any{JsonObject{"target": 2, "action": "omit"}}, "edit retained")
	if _, ok := asObject(entries[2])["model"]; ok {
		t.Fatal("display-only entry gained model")
	}
	projected := must(child.Context(bg))
	equal(t, projected.Head.Id, Id(11), "newest head")
	if slices.ContainsFunc(projected.Entries, func(entry Entry) bool { return entry.Id == 3 }) {
		t.Fatal("model context retained inner head")
	}
	check(t, child.Reset(bg, nil))
	fresh := must(child.Watch(bg))
	defer fresh.Stop()
	folded := collector.view
	for _, envelope := range collector.Envelopes() {
		folded = must(ApplyEnvelope(folded, envelope))
	}
	equal(t, folded["entries"], fresh.View["entries"], "reset fold")
	equal(t, len(arr(fresh.View, "entries")), 1, "reset range")
}

func TestStorageActiveTranscriptCrossesOlderPageHead(t *testing.T) {
	storage := NewMemoryStorage()
	writes := historyConversationWrites(Conversation{Id: 1})
	for id := Id(2); id <= 602; id++ {
		writes = append(writes, Write{Type: WriteEntry, Entry: &Entry{Id: id, ConversationId: 1, Kind: "history"}})
	}
	writes = append(writes, Write{Type: WriteEntry, Entry: &Entry{Id: 603, ConversationId: 1, Kind: "pi.summary", Head: new(Id(100))}})
	must(storage.Commit(bg, writes))
	h := historyHarness(t, storage)
	watch := must(must(h.Root(bg)).Watch(bg))
	defer watch.Stop()
	ids := historyViewIDs(watch.View)
	equal(t, len(ids), 603-100+1, "inclusive source head range")
	equal(t, ids[0], Id(100), "older-page start")
	equal(t, ids[len(ids)-1], Id(603), "newest head")
}

func TestStorageNewestEditWinsButViewRetainsBoth(t *testing.T) {
	storage := NewMemoryStorage()
	writes := historyConversationWrites(Conversation{Id: 1})
	entries := []Entry{{Id: 2, ConversationId: 1, Kind: "pi.user", Model: []JsonObject{{"role": "user", "content": "original", "timestamp": 1}}}, {Id: 3, ConversationId: 1, Kind: "edit.one", Edits: []ContextEdit{{Target: 2, Action: "replace", Messages: []JsonObject{{"role": "user", "content": "older edit", "timestamp": 2}}}}}, {Id: 4, ConversationId: 1, Kind: "edit.two", Edits: []ContextEdit{{Target: 2, Action: "replace", Messages: []JsonObject{{"role": "user", "content": "newest edit", "timestamp": 3}}}}}}
	for _, entry := range entries {
		writes = append(writes, Write{Type: WriteEntry, Entry: &entry})
	}
	must(storage.Commit(bg, writes))
	h := historyHarness(t, storage)
	root := must(h.Root(bg))
	equal(t, must(root.Context(bg)).Messages[0]["content"], "newest edit", "latest edit wins")
	watch := must(root.Watch(bg))
	defer watch.Stop()
	shown := arr(watch.View, "entries")
	equal(t, len(shown), len(entries), "every edit retained")
	for i, entry := range entries {
		var want any
		if entry.Edits != nil {
			check(t, json.Unmarshal(mustJSON(entry.Edits), &want))
		}
		equal(t, asObject(shown[i])["edits"], want, "verbatim edit")
	}
}

func TestStorageOrphansOnlyUnknownLiveTasks(t *testing.T) {
	storage := NewMemoryStorage()
	writes := historyConversationWrites(Conversation{Id: 1})
	for _, task := range []Task{{Id: 2, ConversationId: 1, Kind: "removed.kind", Status: TaskRunning, After: []Id{}, Owns: []Id{}}, {Id: 3, ConversationId: 1, Kind: "removed.kind", Status: TaskTerminal, Outcome: &Outcome{Status: OutcomeCompleted, Result: JsonObject{"retained": true}}, After: []Id{}, Owns: []Id{}}} {
		writes = append(writes, Write{Type: WriteTask, Task: &task})
	}
	must(storage.Commit(bg, writes))
	h := historyHarness(t, storage)
	equal(t, must(h.WaitForTask(bg, 2)).Outcome.Status, OutcomeOrphaned, "unknown live task orphaned")
	retained := must(h.GetTask(bg, 3))
	equal(t, retained.Outcome.Status, OutcomeCompleted, "terminal outcome retained")
	equal(t, retained.Outcome.Result, JsonObject{"retained": true}, "retained result")
}

func TestStorageTerminalDependencySurvivesRetirementAndReopen(t *testing.T) {
	kind := quickKind(t, "spec.retained.dependency", func(task Task) JsonValue { return JsonObject{"value": asObject(task.Input)["value"]} })
	env := openEnv(t, openOptions{backend: "jsonl", taskKinds: []*Kind{kind}})
	first := createTestTask(t, env, kind, JsonObject{"value": 1})
	env.untilTerminal(first.Id)
	for value := 2; value <= 30; value++ {
		ref := must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (TaskRef, error) {
			return tx.CreateTask(kind, JsonObject{"value": value}, TaskOptions{ConversationId: new(Id(1)), Background: true, After: []Id{first.Id}})
		}))
		env.untilTerminal(ref.Id)
	}
	env.crash()
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, taskKinds: []*Kind{kind}})
	ref := must(HostCommit(bg, next.root, func(_ context.Context, tx *Tx) (TaskRef, error) {
		return tx.CreateTask(kind, JsonObject{"value": 31}, TaskOptions{ConversationId: new(Id(1)), Background: true, After: []Id{first.Id}})
	}))
	task := next.untilTerminal(ref.Id)
	equal(t, task.Outcome.Status, OutcomeCompleted, "new dependency completed")
	equal(t, task.Outcome.Result, JsonObject{"value": 31}, "new dependency result")
	retained := must(next.h.GetTask(bg, first.Id))
	equal(t, retained.Outcome.Status, OutcomeCompleted, "old outcome")
	equal(t, retained.Outcome.Result, JsonObject{"value": 1}, "old result")
}
