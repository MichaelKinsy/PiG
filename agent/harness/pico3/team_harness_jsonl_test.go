package pico3

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Source: harness/pico3/atomicity.test.ts. Storage is reopened before a scheduler
// can redo a missing commit, so a half-published checkpoint cannot be hidden.
func TestHarnessPicoJSONLPublicationRecovery(t *testing.T) {
	t.Parallel()
	for _, cut := range []string{"intact", "main", "task", "sticky", "torn-main-tail", "torn-task-tail", "torn-sticky-tail"} {
		t.Run(cut, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			open := func() *JsonlStorage {
				storage := must(OpenJsonlStorage(ctx, dir, JsonlOptions{Fsync: new(false)}))
				t.Cleanup(func() { check(t, storage.Close(ctx)) })
				return storage
			}
			storage := open()
			must(storage.Commit(ctx, []Write{
				{Type: WriteConversation, Conversation: &Conversation{Id: 1}},
				{Type: WriteTask, Task: &Task{Id: 2, ConversationId: 1, Kind: "effect", Status: TaskPending}},
				{Type: WriteDoc, Ref: StickyDoc(1), Ops: []Op{{"r", JsonObject{"mark": "initial"}}}},
			}))
			writes := []Write{
				{Type: WriteTaskPatch, Patch: &TaskPatch{Id: 2, Status: new(TaskRunning), Checkpoint: Checkpoint{"phase": "started"}}},
				{Type: WriteDoc, Ref: StickyDoc(1), Ops: []Op{{"s", []any{"mark"}, "published"}}},
			}
			seq := must(storage.Commit(ctx, writes))
			check(t, storage.Close(ctx))
			assertAtomicityMarker(t, dir, 2)
			files := map[string]string{"main": "main.jsonl", "task": "task-2.jsonl", "sticky": "sticky-1.jsonl"}
			var tornFile string
			var before []byte
			if key, torn := strings.CutPrefix(cut, "torn-"); torn {
				key = strings.TrimSuffix(key, "-tail")
				tornFile = filepath.Join(dir, files[key])
				before = must(os.ReadFile(tornFile))
				check(t, os.WriteFile(tornFile, append(bytes.Clone(before), []byte(`{"seq":999,"maxId":999,"writes":`)...), 0o600))
			} else if file, exists := files[cut]; exists {
				chopLastRecord(t, filepath.Join(dir, file))
			}
			reopened, err := OpenJsonlStorage(ctx, dir, JsonlOptions{Fsync: new(false)})
			if cut == "task" || cut == "sticky" {
				if err == nil {
					check(t, reopened.Close(ctx))
					t.Fatal("published missing sidecar accepted")
				}
				if !strings.Contains(err.Error(), "missing record for published sequence") {
					t.Fatalf("unexpected open error: %v", err)
				}
				return
			}
			check(t, err)
			t.Cleanup(func() { check(t, reopened.Close(ctx)) })
			task := must(reopened.Task(ctx, 2))
			mark := must(reopened.Doc(ctx, StickyDoc(1)))["mark"]
			if cut == "main" {
				equal(t, task.Status, TaskPending, "unpublished task status")
				equal(t, task.Checkpoint, Checkpoint(nil), "unpublished checkpoint")
				equal(t, mark, "initial", "unpublished sticky state")
				equal(t, must(reopened.Commit(ctx, writes)), seq, "unconfirmed sequence safely reused")
			} else {
				equal(t, task.Status, TaskRunning, "published status")
				equal(t, task.Checkpoint, Checkpoint{"phase": "started"}, "published checkpoint")
				equal(t, mark, "published", "published sticky state")
			}
			if tornFile != "" {
				equal(t, string(must(os.ReadFile(tornFile))), string(before), "only unterminated bytes removed")
			}
			check(t, reopened.Close(ctx))
			again := open()
			equal(t, must(again.Task(ctx, 2)).Checkpoint, Checkpoint{"phase": "started"}, "checkpoint on second reopen")
			equal(t, must(again.Doc(ctx, StickyDoc(1)))["mark"], "published", "sticky on second reopen")
		})
	}
}

// Every record carries the committed ID horizon, even when it creates no row.
// A retired task remains terminal after its intermediate sidecar is unlinked.
func TestHarnessPicoJSONLRetirementAndHighWater(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	storage := must(OpenJsonlStorage(ctx, dir, JsonlOptions{Fsync: new(false)}))
	t.Cleanup(func() { check(t, storage.Close(ctx)) })
	must(storage.Commit(ctx, []Write{{Type: WriteTask, Task: &Task{Id: 2, ConversationId: 1, Kind: "effect", Status: TaskPending}}}))
	must(storage.Commit(ctx, []Write{{Type: WriteTaskPatch, Patch: &TaskPatch{Id: 2, Status: new(TaskRunning), Checkpoint: Checkpoint{"phase": "started"}}}}))
	file := filepath.Join(dir, "task-2.jsonl")
	stale := must(os.ReadFile(file))
	allocated := storage.MintId()
	must(storage.Commit(ctx, []Write{{Type: WriteTaskPatch, Patch: &TaskPatch{Id: 2, Status: new(TaskTerminal), Outcome: &Outcome{Status: OutcomeCompleted, Result: "kept"}}}}))
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("terminal sidecar not retired: %v", err)
	}
	check(t, storage.Close(ctx))
	// A crash may leave an old sidecar after the terminal record is published.
	check(t, os.WriteFile(file, stale, 0o600))
	reopened := must(OpenJsonlStorage(ctx, dir, JsonlOptions{Fsync: new(false)}))
	t.Cleanup(func() { check(t, reopened.Close(ctx)) })
	task := must(reopened.Task(ctx, 2))
	equal(t, task.Status, TaskTerminal, "stale sidecar cannot resurrect")
	equal(t, task.Outcome.Result, "kept", "terminal outcome retained")
	equal(t, reopened.MintId(), allocated+1, "committed allocation horizon retained without a created row")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar not removed: %v", err)
	}
	check(t, reopened.Close(ctx))
	again := must(OpenJsonlStorage(ctx, dir, JsonlOptions{Fsync: new(false)}))
	t.Cleanup(func() { check(t, again.Close(ctx)) })
	equal(t, must(again.Task(ctx, 2)).Outcome.Result, "kept", "missing retired sidecar allowed")
	equal(t, again.MintId(), allocated+1, "uncommitted allocation may be reused")
}
