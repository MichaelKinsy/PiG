// Ports packages/durable/test/harness-context.test.ts.

package harness

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type contextSetup struct {
	harness Harness
	root    Conversation
	t       *testing.T
}

func setupContext(t *testing.T) contextSetup {
	harness, _ := openHarness(t, storage.NewMemoryStorage(), nil)
	root, err := harness.Root(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	return contextSetup{harness: harness, root: root, t: t}
}

func (setup contextSetup) append(draft durable.EntryDraft) durable.EntryRecord {
	setup.t.Helper()
	entry, err := durable.Commit(testContext, setup.root, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(setup.root.Id(), draft)
	})
	if err != nil {
		setup.t.Fatal(err)
	}
	return entry
}

func (setup contextSetup) message(model ai.Message, kind ...string) durable.EntryRecord {
	setup.t.Helper()
	entryKind := "message"
	if len(kind) > 0 {
		entryKind = kind[0]
	}
	return setup.append(durable.EntryDraft{Kind: entryKind, Model: []ai.Message{model}})
}

func (setup contextSetup) view(conversation ...Conversation) durable.ContextView {
	setup.t.Helper()
	target := setup.root
	if len(conversation) > 0 {
		target = conversation[0]
	}
	view, err := target.Context(testContext, nil)
	if err != nil {
		setup.t.Fatal(err)
	}
	return view
}

func entryIds(entries []durable.EntryRecord) []durable.EntryId {
	ids := []durable.EntryId{}
	for _, entry := range entries {
		ids = append(ids, entry.Id)
	}
	return ids
}

func expectIds[Id comparable](t *testing.T, got, want []Id) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestConversationContext(t *testing.T) {
	t.Run("returns the whole transcript without a head and excludes model-less entries from messages", func(t *testing.T) {
		setup := setupContext(t)
		first := setup.message(user("hi"))
		note := setup.append(durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "display only"}})
		answer := setup.message(assistant("hello"))
		view := setup.view()
		if view.Head != nil {
			t.Fatal("head is set")
		}
		expectIds(t, entryIds(view.Entries), []durable.EntryId{first.Id, note.Id, answer.Id})
		expectStrings(t, describeMessages(view.Messages), []string{"user:hi", "assistant:hello"})
	})

	t.Run("excludes aborted, error, and deferred assistant messages but keeps their raw entries", func(t *testing.T) {
		setup := setupContext(t)
		setup.message(user("q"))
		aborted := setup.message(assistant("partial", assistantOptions{stopReason: "aborted"}))
		setup.message(assistant("failed", assistantOptions{stopReason: "error"}))
		setup.message(assistant("later", assistantOptions{stopReason: "deferred"}))
		setup.message(assistant("done", assistantOptions{stopReason: "length"}))
		view := setup.view()
		if len(view.Entries) != 5 || view.Entries[1].Id != aborted.Id {
			t.Fatalf("entries %v", entryIds(view.Entries))
		}
		expectStrings(t, describeMessages(view.Messages), []string{"user:q", "assistant:done"})
	})

	t.Run("resolves self heads and uses the newest head marker", func(t *testing.T) {
		setup := setupContext(t)
		setup.message(user("old"))
		reset := setup.append(durable.EntryDraft{Kind: "reset", HeadSelf: true, Model: []ai.Message{user("fresh start")}})
		if reset.Head == nil || *reset.Head != reset.Id {
			t.Fatalf("reset head %v", reset.Head)
		}
		after := setup.message(assistant("after reset"))
		view := setup.view()
		if view.Head == nil || view.Head.Id != reset.Id {
			t.Fatal("head is not the reset")
		}
		expectIds(t, entryIds(view.Entries), []durable.EntryId{reset.Id, after.Id})
		expectStrings(t, describeMessages(view.Messages), []string{"user:fresh start", "assistant:after reset"})

		// A compaction summary heads an earlier kept entry; older head markers in range drop out.
		summary := setup.append(durable.EntryDraft{Kind: "summary", Head: new(after.Id), Model: []ai.Message{user("summary")}})
		tail := setup.message(user("next"))
		view = setup.view()
		if view.Head == nil || view.Head.Id != summary.Id {
			t.Fatal("head is not the summary")
		}
		expectIds(t, entryIds(view.Entries), []durable.EntryId{summary.Id, after.Id, tail.Id})
		expectStrings(t, describeMessages(view.Messages), []string{"user:summary", "assistant:after reset", "user:next"})
	})

	t.Run("applies the newest edit per target within the active range", func(t *testing.T) {
		setup := setupContext(t)
		first := setup.message(user("first"))
		second := setup.message(user("second"))
		replace := func(target durable.EntryId, text string) durable.EntryDraft {
			return durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: target, Action: durable.EditReplace, Messages: []ai.Message{user(text)}}}}
		}
		setup.append(replace(first.Id, "first v2"))
		setup.append(replace(first.Id, "first v3"))
		setup.append(durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: second.Id, Action: durable.EditOmit}}})
		view := setup.view()
		if len(view.Entries) != 5 {
			t.Fatalf("entries %d", len(view.Entries))
		}
		expectStrings(t, describeMessages(view.Messages), []string{"user:first v3"})

		// Edits before the active range no longer apply.
		reset := setup.append(durable.EntryDraft{Kind: "reset", Head: new(second.Id)})
		view = setup.view()
		if view.Head == nil || view.Head.Id != reset.Id {
			t.Fatal("head is not the reset")
		}
		expectStrings(t, describeMessages(view.Messages), []string{})
		setup.append(replace(second.Id, "second v2"))
		view = setup.view()
		expectStrings(t, describeMessages(view.Messages), []string{"user:second v2"})
	})

	t.Run("keeps positional system messages and orders tool results by call order", func(t *testing.T) {
		setup := setupContext(t)
		setup.message(system(ai.PromptSection{Name: "preamble", Value: new("You help.")}), "pi.system")
		setup.message(user("run tools"))
		setup.message(assistant("calling", assistantOptions{calls: []string{"b", "a"}}))
		setup.message(toolResult("a"))
		setup.append(durable.EntryDraft{Kind: "pi.system", Model: []ai.Message{system(ai.PromptSection{Name: "cwd", Value: new("/repo")})}})
		setup.message(toolResult("b"))
		setup.message(toolResult("zz"))
		setup.message(assistant("done"))
		expectStrings(t, describeMessages(setup.view().Messages), []string{
			"system:preamble",
			"user:run tools",
			"assistant:calling",
			"result:b:result b",
			"result:a:result a",
			"system:cwd",
			"assistant:done",
		})
	})

	t.Run("synthesizes missing tool results after a fork and drops results cut from their call", func(t *testing.T) {
		setup := setupContext(t)
		setup.message(user("go"))
		call := setup.message(assistant("calling", assistantOptions{calls: []string{"x", "y"}}))
		setup.message(toolResult("x"))
		second := setup.message(toolResult("y"))
		child, err := setup.root.Fork(testContext, call.Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			t.Fatal(err)
		}
		childView := setup.view(child)
		expectStrings(t, describeMessages(childView.Messages), []string{"user:go", "assistant:calling", "result:x:error", "result:y:error"})
		missing, ok := childView.Messages[2].(ai.ToolResultMessage)
		if !ok || missing.ToolName != "tool-x" || plainObject(missing.Details)["reason"] != "missing_result" {
			t.Fatalf("missing %+v", childView.Messages[2])
		}

		// A head between a call and its results leaves stray results that are not sent.
		setup.append(durable.EntryDraft{Kind: "reset", Head: new(second.Id)})
		parentView := setup.view()
		expectStrings(t, describeMessages(parentView.Messages), []string{})
		var kinds []string
		for _, entry := range parentView.Entries {
			kinds = append(kinds, entry.Kind)
		}
		expectStrings(t, kinds, []string{"reset", "message"})
	})

	// Pi 1.1.0 (#10542): packages/durable/src/harness/context.ts leadWithSystem.
	t.Run("leads with a system message that only user messages precede, and keeps later ones in place", func(t *testing.T) {
		setup := setupContext(t)
		setup.message(user("first"))
		setup.message(user("steered"))
		setup.message(system(ai.PromptSection{Name: "preamble", Value: new("You help.")}), "pi.system")
		setup.message(assistant("answer"))
		setup.message(user("next"))
		setup.message(system(ai.PromptSection{Name: "cwd", Value: new("/repo")}), "pi.system")
		view := setup.view()
		expectStrings(t, describeMessages(view.Messages), []string{
			"system:preamble", "user:first", "user:steered", "assistant:answer", "user:next", "system:cwd",
		})
		// Stored order and contributions stay as committed.
		var contributed []ai.Message
		for _, contribution := range view.Contributions {
			contributed = append(contributed, contribution...)
		}
		expectStrings(t, describeMessages(contributed)[:3], []string{"user:first", "user:steered", "system:preamble"})

		// After a reset, the baseline written after the handoff leads too.
		setup.append(durable.EntryDraft{Kind: "reset", HeadSelf: true, Model: []ai.Message{user("handoff")}})
		setup.message(system(ai.PromptSection{Name: "preamble", Value: new("Baseline.")}), "pi.system")
		expectStrings(t, describeMessages(setup.view().Messages), []string{"system:preamble", "user:handoff"})
	})

	t.Run("reads the context as of an earlier entry, as a fork at that entry starts", func(t *testing.T) {
		setup := setupContext(t)
		first := setup.message(user("first"))
		call := setup.message(assistant("calling", assistantOptions{calls: []string{"x", "y"}}))
		result := setup.message(toolResult("x"))
		setup.message(toolResult("y"))
		edit := setup.append(durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: first.Id, Action: durable.EditReplace, Messages: []ai.Message{user("first v2")}}}})
		reset := setup.append(durable.EntryDraft{Kind: "reset", HeadSelf: true, Model: []ai.Message{user("fresh start")}})
		tail := setup.message(assistant("after reset"))

		contextAt := func(conversation Conversation, at *durable.EntryId) durable.ContextView {
			t.Helper()
			var options *durable.ContextOptions
			if at != nil {
				options = &durable.ContextOptions{At: at}
			}
			view, err := conversation.Context(testContext, options)
			if err != nil {
				t.Fatal(err)
			}
			return view
		}
		for _, at := range []durable.EntryRecord{first, call, result, edit, reset, tail} {
			fork, err := setup.root.Fork(testContext, at.Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := contextAt(setup.root, &at.Id), contextAt(fork, nil); !reflect.DeepEqual(got, want) {
				t.Fatalf("context at entry %d is %+v, its fork starts with %+v", at.Id, got, want)
			}
		}
		// Stepped back before the edit and the reset: neither applies, and a cut call gets synthesized results.
		expectStrings(t, describeMessages(contextAt(setup.root, &call.Id).Messages), []string{"user:first", "assistant:calling", "result:x:error", "result:y:error"})
		if got, want := contextAt(setup.root, &tail.Id), contextAt(setup.root, nil); !reflect.DeepEqual(got, want) {
			t.Fatalf("context at the tail %+v differs from the whole context %+v", got, want)
		}
		if got, want := must1(setup.root.Context(testContext, &durable.ContextOptions{})), contextAt(setup.root, nil); !reflect.DeepEqual(got, want) {
			t.Fatalf("empty options changed the context: %+v vs %+v", got, want)
		}

		other, err := setup.root.Fork(testContext, first.Id, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Context(testContext, &durable.ContextOptions{At: &tail.Id}); err == nil || !strings.Contains(err.Error(), "is not visible") {
			t.Fatalf("a context at an entry the conversation does not see returned %v, want \"is not visible\"", err)
		}
	})
}

func must1[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
