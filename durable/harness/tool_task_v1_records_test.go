package harness

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// Pi 1.2.0 test/harness-nested-tools.test.ts "tool task version 1 records" (:1276-1342): a pi.tool task stored by version 1
// (`{ assistant, callId }` input, no `kind`) migrates when the Harness loads it and runs, or settles interrupted, as a
// model-issued call.

// storeToolTaskVersion stores a live tool task for call c1 of an assistant entry, closes the Harness, and rewrites its
// record as `version` stored it, with checkpoint: version 1 had `{ assistant, callId }` input, version 2 added `kind`. It
// returns the path and the task ID.
func storeToolTaskVersion(t *testing.T, setup *chatState, checkpoint durable.JsonValue, version int) (string, durable.TaskId) {
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
	if version == 1 {
		delete(input, "kind")
	}
	record.Version = version
	record.Input = input
	record.State.Checkpoint = &checkpoint
	if _, err := durabletest.CommitWrites(testContext, store, []durable.StorageWrite{durable.TaskWrite{Value: *record}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(testContext); err != nil {
		t.Fatal(err)
	}
	return path, id
}

// expectSettledModelToolTask is Pi's toMatchObject of a settled stored tool task: its input is a model-issued call of c1 with the
// number of its assistant entry (`assistant: any(Number)`, or the exact version 2 shape when exact is set), and its outcome has
// status and a model result.
func expectSettledModelToolTask(t *testing.T, settled durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], exact bool, status string) {
	t.Helper()
	var input, outcome map[string]any
	if err := json.Unmarshal(must(json.Marshal(settled.Input)), &input); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(must(json.Marshal(settled.State.Outcome)), &outcome); err != nil {
		t.Fatal(err)
	}
	if _, numbered := input["assistant"].(float64); input["kind"] != "model" || input["callId"] != "c1" || (exact && (!numbered || len(input) != 3)) {
		t.Fatalf("settled input %v, want {kind: model, assistant: <number>, callId: c1}", input)
	}
	result, _ := outcome["result"].(map[string]any)
	if outcome["status"] != status || result["kind"] != "model" {
		t.Fatalf("settled outcome %v, want status %s with a model result", outcome, status)
	}
}

func reopenToolTaskVersion(t *testing.T, setup *chatState, path string, id durable.TaskId, version int) (Harness, Conversation, durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) {
	t.Helper()
	harness, root := openChat(t, openSqlite(t, path), setup)
	stored := must(harness.GetTask(testContext, id))
	if stored == nil || stored.Version != version {
		t.Fatalf("the stored record = %+v, want version %d", stored, version)
	}
	harness.Resume()
	return harness, root, must(harness.WaitForTask(testContext, id))
}

// harness-nested-tools.test.ts:1293 it("migrates a pending version 1 task and runs it as a model-issued call")
func TestAPendingVersion1ToolTaskMigratesAndRunsAsAModelIssuedCall(t *testing.T) {
	setup := chatSetup(t)
	runs := 0
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs++
		text, _ := args.(map[string]any)["text"].(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
	}))
	path, id := storeToolTaskVersion(t, setup, map[string]any{"phase": "call"}, 1)
	harness, root, settled := reopenToolTaskVersion(t, setup, path, id, 1)
	defer func() { _ = harness.Close(testContext) }()
	if settled.Version != 4 {
		t.Fatalf("settled version %d, want 4", settled.Version)
	}
	expectSettledModelToolTask(t, settled, false, "completed")
	results := tlResults(allEntries(t, root))
	if runs != 1 || len(results) != 1 || results[0].ToolCallID != "c1" || tlResultText(results[0]) != "echo old" {
		t.Fatalf("runs %d results %+v, want one run and c1 \"echo old\"", runs, results)
	}
}

// harness-nested-tools.test.ts:1311 it("migrates a version 1 task interrupted after intent and settles it interrupted")
func TestAVersion1ToolTaskInterruptedAfterIntentSettlesInterrupted(t *testing.T) {
	setup := chatSetup(t)
	runs := 0
	addTool(t, setup.Registry, tlTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs++
		return durable.ToolExecutionResult{}, nil
	}))
	path, id := storeToolTaskVersion(t, setup, map[string]any{"phase": "execute", "arguments": map[string]any{"text": "old"}, "replay": "unsafe"}, 1)
	harness, root, settled := reopenToolTaskVersion(t, setup, path, id, 1)
	defer func() { _ = harness.Close(testContext) }()
	if settled.Version != 4 {
		t.Fatalf("settled version %d, want 4", settled.Version)
	}
	var outcome map[string]any
	if err := json.Unmarshal(must(json.Marshal(settled.State.Outcome)), &outcome); err != nil {
		t.Fatal(err)
	}
	if result, _ := outcome["result"].(map[string]any); outcome["status"] != "failed" || result["kind"] != "model" {
		t.Fatalf("settled outcome %v, want status failed with a model result", outcome)
	}
	results := tlResults(allEntries(t, root))
	if runs != 0 || len(results) != 1 || results[0].ToolCallID != "c1" || !results[0].IsError || !strings.Contains(tlResultText(results[0]), "Tool echo was interrupted") {
		t.Fatalf("runs %d results %+v, want the tool not run and an interrupted error result", runs, results)
	}
}

// harness-nested-tools.test.ts:1330 it("reserves a stored version 2 task under version 4 and runs it as a model-issued call") (#11043)
func TestAVersion2ToolTaskIsReservedUnderVersion4AndRunsAsAModelIssuedCall(t *testing.T) {
	setup := chatSetup(t)
	runs := 0
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		runs++
		text, _ := args.(map[string]any)["text"].(string)
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
	}))
	path, id := storeToolTaskVersion(t, setup, map[string]any{"phase": "call"}, 2)
	harness, root, settled := reopenToolTaskVersion(t, setup, path, id, 2)
	defer func() { _ = harness.Close(testContext) }()
	if settled.Version != 4 {
		t.Fatalf("settled version %d, want 4", settled.Version)
	}
	expectSettledModelToolTask(t, settled, true, "completed")
	results := tlResults(allEntries(t, root))
	if runs != 1 || len(results) != 1 || results[0].ToolCallID != "c1" || tlResultText(results[0]) != "echo old" {
		t.Fatalf("runs %d results %+v, want one run and c1 \"echo old\"", runs, results)
	}
}
