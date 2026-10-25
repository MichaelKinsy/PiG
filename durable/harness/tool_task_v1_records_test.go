package harness

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 1.2.0 test/harness-nested-tools.test.ts "tool task version 1 records" (:1276-1342): a pi.tool task stored by version 1
// (`{ assistant, callId }` input, no `kind`) migrates when the Harness loads it and runs, or settles interrupted, as a
// model-issued call.

// storeToolTaskVersion1 stores a live tool task for call c1 of an assistant entry, closes the Harness, and rewrites its
// record as version 1 stored it: the input without `kind`, version 1, and checkpoint. It returns the path and the task ID.
func storeToolTaskVersion1(t *testing.T, setup *chatState, checkpoint durable.JsonValue) (string, durable.TaskId) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.sqlite")
	// No Resume, so the Harness never starts scheduling and the task stays pending.
	harness, root := openChat(t, openSqlite(t, path), setup)
	id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		entry, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "pi.assistant", Model: []ai.Message{ai.AssistantMessage{
			Content:    []ai.AssistantContentBlock{ai.ToolCall{ID: "c1", Name: "echo", Arguments: ai.JsonObject{"text": "old"}}},
			StopReason: "toolUse", Timestamp: 1,
		}}})
		if err != nil {
			return 0, err
		}
		return durable.CreateTask(tx, ToolTask, ToolTaskInput{Kind: ToolTaskModel, Assistant: entry.Id, CallId: "c1"}, durable.TaskOptions{Ownership: conversationOwned})
	}))
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
	store := openSqlite(t, path)
	record := must(store.Task(testContext, id))
	raw := must(json.Marshal(record.Input))
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	delete(input, "kind")
	record.Version = 1
	record.Input = input
	record.State.Checkpoint = &checkpoint
	if _, err := store.Commit(testContext, []durable.StorageWrite{durable.TaskWrite{Value: *record}}, func(durable.Seq) {}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(testContext); err != nil {
		t.Fatal(err)
	}
	return path, id
}

func reopenToolTaskVersion1(t *testing.T, setup *chatState, path string, id durable.TaskId) (Harness, Conversation, durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
	t.Helper()
	harness, root := openChat(t, openSqlite(t, path), setup)
	stored := must(harness.GetTask(testContext, id))
	if stored == nil || stored.Version != 1 {
		t.Fatalf("the stored record = %+v, want version 1", stored)
	}
	harness.Resume()
	return harness, root, must(harness.WaitForTask(testContext, id))
}

// harness-nested-tools.test.ts:1308 it("migrates a pending version 1 task and runs it as a model-issued call")
func TestAPendingVersion1ToolTaskMigratesAndRunsAsAModelIssuedCall(t *testing.T) {
	setup := chatSetup(t)
	runs := 0
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs++
		text, _ := args.(map[string]any)["text"].(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
	}))
	path, id := storeToolTaskVersion1(t, setup, map[string]any{"phase": "call"})
	harness, root, settled := reopenToolTaskVersion1(t, setup, path, id)
	defer func() { _ = harness.Close(testContext) }()
	input := must(json.Marshal(settled.Input))
	outcome := must(json.Marshal(settled.State.Outcome))
	if settled.Version != 2 || !strings.Contains(string(input), `"kind":"model"`) || !strings.Contains(string(input), `"callId":"c1"`) ||
		!strings.Contains(string(outcome), `"status":"completed"`) || !strings.Contains(string(outcome), `"kind":"model"`) {
		t.Fatalf("settled version %d input %s outcome %s", settled.Version, input, outcome)
	}
	results := tlResults(allEntries(t, root))
	if runs != 1 || len(results) != 1 || results[0].ToolCallID != "c1" || tlResultText(results[0]) != "echo old" {
		t.Fatalf("runs %d results %+v, want one run and c1 \"echo old\"", runs, results)
	}
}

// harness-nested-tools.test.ts:1326 it("migrates a version 1 task interrupted after intent and settles it interrupted")
func TestAVersion1ToolTaskInterruptedAfterIntentSettlesInterrupted(t *testing.T) {
	setup := chatSetup(t)
	runs := 0
	addTool(t, setup.Registry, tlTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs++
		return durable.ToolExecutionResult{}, nil
	}))
	path, id := storeToolTaskVersion1(t, setup, map[string]any{"phase": "execute", "arguments": map[string]any{"text": "old"}, "replay": "unsafe"})
	harness, root, settled := reopenToolTaskVersion1(t, setup, path, id)
	defer func() { _ = harness.Close(testContext) }()
	outcome := must(json.Marshal(settled.State.Outcome))
	if settled.Version != 2 || !strings.Contains(string(outcome), `"status":"failed"`) || !strings.Contains(string(outcome), `"kind":"model"`) {
		t.Fatalf("settled version %d outcome %s", settled.Version, outcome)
	}
	results := tlResults(allEntries(t, root))
	if runs != 0 || len(results) != 1 || results[0].ToolCallID != "c1" || !results[0].IsError || !strings.Contains(tlResultText(results[0]), "Tool echo was interrupted") {
		t.Fatalf("runs %d results %+v, want the tool not run and an interrupted error result", runs, results)
	}
}
