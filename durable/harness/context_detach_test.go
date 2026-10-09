package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
)

// A storage that retains decoded entries shares them with its readers internally; the Context a caller receives must
// still be its own, as a view decoded from the table would be.
func TestContextViewReturnedToACallerIsDetachedFromWhatLaterReadsSee(t *testing.T) {
	harness, _ := openHarness(t, cacheTestStorage(t), nil)
	root, err := harness.Root(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, draft := range []durable.EntryDraft{
		{Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("question"), Timestamp: 1}}, Data: map[string]any{"nested": []any{"a", map[string]any{"k": "v"}}}},
		{Kind: "message", Model: []ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}, ai.ToolCall{ID: "c1", Name: "lookup", Arguments: ai.JsonObject{"n": 1.0}}}, StopReason: "toolUse", Timestamp: 2}}},
		{Kind: "message", Model: []ai.Message{ai.ToolResultMessage{ToolCallID: "c1", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "found"}}, Timestamp: 3}}},
	} {
		if _, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(root.Id(), draft)
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := root.Context(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	pristine := detach.ContextView(first)
	damaged, err := root.Context(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Overwrite everything the view reaches.
	for index := range damaged.Entries {
		damaged.Entries[index].Kind = "damaged"
		switch data := damaged.Entries[index].Data.(type) {
		case map[string]any:
			data["nested"].([]any)[1].(map[string]any)["k"] = "damaged"
			data["added"] = true
		case *delta.JsonObject:
			data.Value("nested").([]any)[1].(*delta.JsonObject).Set("k", "damaged")
			data.Set("added", true)
		}
		for _, message := range damaged.Entries[index].Model {
			if assistant, ok := message.(ai.AssistantMessage); ok {
				assistant.Content[0] = ai.TextContent{Text: "damaged"}
				assistant.Content[1].(ai.ToolCall).Arguments["n"] = 99.0
			}
		}
	}
	for index := range damaged.Messages {
		if assistant, ok := damaged.Messages[index].(ai.AssistantMessage); ok {
			assistant.Content[0] = ai.TextContent{Text: "damaged"}
			assistant.Content[1].(ai.ToolCall).Arguments["n"] = 99.0
		}
	}
	for _, contribution := range damaged.Contributions {
		for _, message := range contribution {
			if assistant, ok := message.(ai.AssistantMessage); ok {
				assistant.Content[0] = ai.TextContent{Text: "damaged"}
			}
		}
	}
	again, err := root.Context(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, pristine) {
		t.Fatal("a caller's changes to its view changed what a later read returns")
	}
}
