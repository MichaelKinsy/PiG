// Ports packages/durable/test/harness-context.test.ts "reads the context as of an earlier entry, as a fork at that entry starts" (#10512).

package harness

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

func TestConversationContextAtReadsTheContextAsAForkAtThatEntryStarts(t *testing.T) {
	setup := setupContext(t)
	first := setup.message(user("first"))
	call := setup.message(assistant("calling", assistantOptions{calls: []string{"x", "y"}}))
	result := setup.message(toolResult("x"))
	setup.message(toolResult("y"))
	edit := setup.append(durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: first.Id, Action: "replace", Messages: []ai.Message{user("first v2")}}}})
	reset := setup.append(durable.EntryDraft{Kind: "reset", HeadSelf: true, Model: []ai.Message{user("fresh start")}})
	tail := setup.message(assistant("after reset"))
	ownerless := ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}

	for name, at := range map[string]durable.EntryRecord{"first": first, "call": call, "result": result, "edit": edit, "reset": reset, "tail": tail} {
		fork, err := setup.root.Fork(testContext, at.Id, ownerless)
		if err != nil {
			t.Fatal(err)
		}
		got, err := setup.root.Context(testContext, &durable.ContextOptions{At: &at.Id})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := setup.view(fork); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: context at the entry %+v, a fork there starts with %+v", name, got, want)
		}
	}

	// Stepped back before the edit and the reset: neither applies, and a cut call gets synthesized results.
	atCall, err := setup.root.Context(testContext, &durable.ContextOptions{At: &call.Id})
	if err != nil {
		t.Fatal(err)
	}
	expectStrings(t, describeMessages(atCall.Messages), []string{"user:first", "assistant:calling", "result:x:error", "result:y:error"})
	atTail, err := setup.root.Context(testContext, &durable.ContextOptions{At: &tail.Id})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(atTail, setup.view()) {
		t.Errorf("context at the newest entry %+v differs from the current %+v", atTail, setup.view())
	}

	other, err := setup.root.Fork(testContext, first.Id, ownerless)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Context(testContext, &durable.ContextOptions{At: &tail.Id}); err == nil || !strings.Contains(err.Error(), "is not visible") {
		t.Errorf("an entry the fork cannot see: err %v, want \"is not visible\"", err)
	}
}
