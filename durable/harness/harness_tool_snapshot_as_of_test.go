package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// types.ts ToolExecutionApi extends DocumentReader (harness/types.ts), and tool.ts:247 wires `snapshotAsOf` to the
// runtime's: a tool reads a rewindable conversation document as of a visible entry, not its latest value. The tool here
// reads the note as of the entry committed with "first" after the note was set to "second".
// Pi: packages/durable/src/harness/tool.ts:247 (snapshotAsOf), packages/durable/test/harness-conversations.test.ts:456
func TestToolExecutionApiSnapshotAsOfReadsTheDocumentAsOfTheEntry(t *testing.T) {
	setup := chatSetup(t)
	var live, asOf durable.JsonObject
	var readErr error
	var entryId durable.EntryId
	addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "peek", Description: "Reads the note", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			rootId := durable.ROOT_CONVERSATION_ID
			if live, readErr = api.SnapshotErased(ctx, noteDoc, rootId); readErr == nil {
				asOf, readErr = api.SnapshotAsOfErased(ctx, noteDoc, entryId, rootId)
			}
			return durable.ToolExecutionResult{}, readErr
		},
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("peek", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		fauxAnswer("done"),
	})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	entry, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
		if err := setNote(tx, root.Id(), "first"); err != nil {
			return durable.EntryRecord{}, err
		}
		return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("m")}})
	})
	if err != nil {
		t.Fatal(err)
	}
	entryId = entry.Id
	if _, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) { return nil, setNote(tx, root.Id(), "second") }); err != nil {
		t.Fatal(err)
	}
	submission := submitWhenBusy(t, root, "go")
	must(submission.Wait(testContext))
	drained(harness)
	if readErr != nil {
		t.Fatalf("snapshot reads failed: %v", readErr)
	}
	expectJSON(t, live, `{"text":"second"}`)
	expectJSON(t, asOf, `{"text":"first"}`)
}
