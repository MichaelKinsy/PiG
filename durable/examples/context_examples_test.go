// Ports packages/durable/test/examples/09-context.ts: transcript history and model context.

package examples_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

func exampleAssistant(text string, stopReason ai.StopReason, calls ...string) ai.AssistantMessage {
	blocks := []ai.AssistantContentBlock{ai.TextContent{Text: text}}
	for _, id := range calls {
		blocks = append(blocks, ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{}})
	}
	if stopReason == "" {
		stopReason = "stop"
		if len(calls) > 0 {
			stopReason = "toolUse"
		}
	}
	return ai.AssistantMessage{Content: blocks, API: "example", Provider: "example", Model: "example", StopReason: stopReason, Timestamp: 2}
}

func exampleToolResult(id string) ai.ToolResultMessage {
	return ai.ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file " + id}}, Timestamp: 3}
}

func exampleUser(text string, timestamp int64) ai.UserMessage {
	return ai.UserMessage{Content: ai.UserText(text), Timestamp: timestamp}
}

// show is the example's one-line rendering of a message.
func show(message ai.Message) string {
	switch typed := message.(type) {
	case ai.UserMessage:
		return "user: " + string(typed.Content.(ai.UserText))
	case ai.SystemMessage:
		sections := map[string]string{}
		for _, section := range typed.Sections {
			if section.Value != nil {
				sections[section.Name] = *section.Value
			}
		}
		// JSON.stringify does not escape <, > or &.
		var encoded strings.Builder
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(sections)
		return "system: " + strings.TrimSuffix(encoded.String(), "\n")
	case ai.AssistantMessage:
		var parts []string
		for _, part := range typed.Content {
			switch block := part.(type) {
			case ai.TextContent:
				parts = append(parts, block.Text)
			case ai.ToolCall:
				parts = append(parts, "call("+block.ID+")")
			default:
				parts = append(parts, "")
			}
		}
		return "assistant: " + strings.Join(parts, " ")
	case ai.ToolResultMessage:
		suffix := ""
		if typed.IsError {
			suffix = " error"
		}
		return fmt.Sprintf("result(%s)%s", typed.ToolCallID, suffix)
	}
	return fmt.Sprintf("%T", message)
}

func shown(messages []ai.Message) []string {
	lines := []string{}
	for _, message := range messages {
		lines = append(lines, show(message))
	}
	return lines
}

func entryKinds(entries []durable.EntryRecord) []string {
	kinds := []string{}
	for _, entry := range entries {
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

// Entries are immutable. `model` holds the messages an entry contributes to the next model request; `data` is for the
// app only. Context turns the stored transcript into those request messages: an entry with a head starts a new
// context, edits replace or omit what an earlier entry contributes, aborted, error and deferred assistant messages
// are not sent, tool results are sent right after their call in call order, and a call without a result gets a
// synthesized error result. A headed summary replaces everything before the entry it points at; Entries pages the
// stored transcript newest first, including inherited parent entries; nothing is ever deleted by heads or edits.
func TestExample09Context(t *testing.T) {
	opened := openHarness(t, harness.CreateRegistry(), nil)
	transcript, err := opened.CreateConversation(background, harness.ConversationCreateOptions{Ownership: ownerless.Ownership})
	if err != nil {
		t.Fatal(err)
	}
	say := func(kind string, model ...ai.Message) durable.EntryRecord {
		t.Helper()
		return commit(t, transcript, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(transcript.Id(), durable.EntryDraft{Kind: kind, Model: model})
		})
	}

	question := say("message", exampleUser("read a and b", 1))
	say("message", exampleAssistant("I crashed", ai.StopReasonAborted)) // stored, never sent
	calls := say("message", exampleAssistant("reading", "", "a", "b"))
	say("message", exampleToolResult("b")) // results finish out of order
	reading := "<cwd>/repo</cwd>"
	say("pi.system", ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "cwd", Value: &reading}}, Timestamp: 4})
	say("message", exampleToolResult("a"))
	say("message", exampleAssistant("a and b look fine", ""))
	commit(t, transcript, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(transcript.Id(), durable.EntryDraft{
			Kind: "edit",
			Data: "user fixed a typo",
			Edits: []durable.ContextEdit{{
				Target:   question.Id,
				Action:   durable.EditReplace,
				Messages: []ai.Message{exampleUser("read files a and b", 1)},
			}},
		})
	})
	commit(t, transcript, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(transcript.Id(), durable.EntryDraft{Kind: "note", Data: "display only"})
	})

	view := must(transcript.Context(background))
	expectEqual(t, "raw active entries", entryKinds(view.Entries), []string{"message", "message", "message", "message", "pi.system", "message", "message", "edit", "note"})
	expectEqual(t, "request messages", shown(view.Messages), []string{
		"user: read files a and b", // the edit replaced the question
		"assistant: reading call(a) call(b)",
		"result(a)", // tool results follow their call, in call order
		"result(b)",
		`system: {"cwd":"<cwd>/repo</cwd>"}`,
		"assistant: a and b look fine", // the aborted assistant message is not sent
	})

	cut, err := transcript.Fork(background, calls.Id, harness.ConversationCreateOptions{Ownership: ownerless.Ownership})
	if err != nil {
		t.Fatal(err)
	}
	// A fork at the tool call has no results yet; Context fills them in.
	expectEqual(t, "fork messages", shown(must(cut.Context(background)).Messages), []string{
		"user: read a and b", // the edit came after the fork point
		"assistant: reading call(a) call(b)",
		"result(a) error",
		"result(b) error",
	})

	// "self" points the head at the summary entry itself.
	commit(t, transcript, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(transcript.Id(), durable.EntryDraft{Kind: "summary", HeadSelf: true, Model: []ai.Message{exampleUser("Summary: a and b are fine.", 5)}})
	})
	view = must(transcript.Context(background))
	if view.Head == nil || view.Head.Kind != "summary" {
		t.Fatalf("head after summary: %+v", view.Head)
	}
	expectEqual(t, "after summary", shown(view.Messages), []string{"user: Summary: a and b are fine."})

	history := must(transcript.Entries(background, durable.EntryQuery{}, 3, nil))
	expectEqual(t, "newest stored entries", entryKinds(history.Items), []string{"summary", "note", "edit"})
	if history.Next == nil {
		t.Fatal("history reports no more entries")
	}
	closeSession(t, opened)
}
