// Ports packages/durable/test/harness-prompt.test.ts (the cases that plan against a Harness conversation).

package harness

// pi: packages/durable/src/harness/prompt.ts

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type plannedEntry struct {
	sections [][2]any // name, *string (nil = null)
	omit     []durable.EntryId
}

func sectionPairs(sections ai.OrderedSections) [][2]any {
	pairs := [][2]any{}
	for _, section := range sections {
		var value any
		if section.Value != nil {
			value = *section.Value
		}
		pairs = append(pairs, [2]any{section.Name, value})
	}
	return pairs
}

func pairs(values ...any) [][2]any {
	out := [][2]any{}
	for i := 0; i < len(values); i += 2 {
		out = append(out, [2]any{values[i], values[i+1]})
	}
	return out
}

func promptRoot(t *testing.T) Conversation {
	harness, _ := openHarness(t, storage.NewMemoryStorage(), nil)
	root, err := harness.Root(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func contextOf(t *testing.T, conversation Conversation) durable.ContextView {
	t.Helper()
	view, err := conversation.Context(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func appendDrafts(t *testing.T, conversation Conversation, drafts []SystemDraft) {
	t.Helper()
	_, err := conversation.Commit(testContext, func(tx durable.Tx) (any, error) {
		for _, draft := range drafts {
			if _, err := durable.TxAppendEntry(tx, durable.SystemEntry, conversation.Id(), draft); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// applyPlan plans against the current context, appends the plan, and checks that replay then yields desired in order.
func applyPlan(t *testing.T, conversation Conversation, desired ...[2]string) []plannedEntry {
	t.Helper()
	drafts := PlanSystemEntries(contextOf(t, conversation), orderedOf(desired...), nil, 7)
	appendDrafts(t, conversation, drafts)
	replayed := entriesOf(ReplaySections(contextOf(t, conversation).Messages))
	want := [][2]string{}
	want = append(want, desired...)
	if !reflect.DeepEqual(replayed, want) {
		t.Fatalf("replayed %q, want %q", replayed, want)
	}
	planned := []plannedEntry{}
	for _, draft := range drafts {
		message := draft.Model[0].(ai.SystemMessage)
		if message.Content != ai.SystemText("") || message.Timestamp != 7 {
			t.Fatalf("message %+v", message)
		}
		entry := plannedEntry{sections: sectionPairs(message.Sections)}
		for _, edit := range draft.Edits {
			if edit.Action != durable.EditOmit {
				t.Fatalf("edit %+v", edit)
			}
			entry.omit = append(entry.omit, edit.Target)
		}
		planned = append(planned, entry)
	}
	return planned
}

func expectPlanned(t *testing.T, got []plannedEntry, want ...plannedEntry) {
	t.Helper()
	if want == nil {
		want = []plannedEntry{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func lastSystemId(t *testing.T, conversation Conversation) durable.EntryId {
	t.Helper()
	page, err := conversation.Entries(testContext, durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Items {
		if entry.Kind == "pi.system" {
			return entry.Id
		}
	}
	t.Fatal("no pi.system entry")
	return 0
}

func marker(t *testing.T, conversation Conversation, head *durable.EntryId) durable.EntryId {
	t.Helper()
	draft := durable.EntryDraft{Kind: "summary", Model: []ai.Message{user("summary")}}
	if head == nil {
		draft.HeadSelf = true
	} else {
		draft.Head = head
	}
	entry, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(conversation.Id(), draft)
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry.Id
}

func kv(key, value string) [2]string { return [2]string{key, value} }

func TestSystemPromptPreparationPlans(t *testing.T) {
	t.Run("emits minimal value patches, removals, and additions", func(t *testing.T) {
		conversation := promptRoot(t)
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1"), kv("b", "2"), kv("c", "3")), plannedEntry{sections: pairs("a", "1", "b", "2", "c", "3")})
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1"), kv("b", "20"), kv("c", "3"), kv("d", "4")), plannedEntry{sections: pairs("b", "20", "d", "4")})
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1"), kv("c", "3"), kv("d", "4")), plannedEntry{sections: pairs("b", nil)})
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1"), kv("c", "3"), kv("d", "4")))
		expectPlanned(t, applyPlan(t, conversation), plannedEntry{sections: pairs("a", nil, "c", nil, "d", nil)})
	})

	t.Run("rewrites order-only changes and re-additions as two entries", func(t *testing.T) {
		conversation := promptRoot(t)
		applyPlan(t, conversation, kv("a", "1"), kv("b", "2"))
		expectPlanned(t, applyPlan(t, conversation, kv("b", "2"), kv("a", "1")),
			plannedEntry{sections: pairs("a", nil, "b", nil)},
			plannedEntry{sections: pairs("b", "2", "a", "1")})
		readded := promptRoot(t)
		applyPlan(t, readded, kv("a", "1"), kv("b", "2"), kv("c", "3"))
		expectPlanned(t, applyPlan(t, readded, kv("a", "1"), kv("c", "3")), plannedEntry{sections: pairs("b", nil)})
		// Patching would append b after c.
		expectPlanned(t, applyPlan(t, readded, kv("a", "1"), kv("b", "2"), kv("c", "3")),
			plannedEntry{sections: pairs("a", nil, "c", nil)},
			plannedEntry{sections: pairs("a", "1", "b", "2", "c", "3")})
	})

	t.Run("rebaselines after a head marker, omitting retained deltas on both sides of it", func(t *testing.T) {
		conversation := promptRoot(t)
		applyPlan(t, conversation, kv("a", "1"), kv("b", "2"))
		if _, err := conversation.Commit(testContext, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "pi.user", Model: []ai.Message{user("hi")}})
		}); err != nil {
			t.Fatal(err)
		}
		applyPlan(t, conversation, kv("a", "1"), kv("b", "20"))
		delta := lastSystemId(t, conversation)
		// The head keeps the delta but cuts its baseline: replay alone would show only b.
		marker(t, conversation, &delta)
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1"), kv("b", "20")), plannedEntry{sections: pairs("a", "1", "b", "20"), omit: []durable.EntryId{delta}})
		baseline := lastSystemId(t, conversation)
		// A system entry follows the marker now, so later changes are ordinary patches.
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1"), kv("b", "21")), plannedEntry{sections: pairs("b", "21")})
		after := lastSystemId(t, conversation)
		// A second marker keeps deltas from both sides of the first one.
		marker(t, conversation, &delta)
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1"), kv("b", "21")), plannedEntry{sections: pairs("a", "1", "b", "21"), omit: []durable.EntryId{delta, baseline, after}})
	})

	t.Run("writes a complete post-head baseline even when replay already matches", func(t *testing.T) {
		conversation := promptRoot(t)
		applyPlan(t, conversation, kv("a", "1"))
		baseline := lastSystemId(t, conversation)
		marker(t, conversation, &baseline)
		expectPlanned(t, applyPlan(t, conversation, kv("a", "1")), plannedEntry{sections: pairs("a", "1"), omit: []durable.EntryId{baseline}})
		marker(t, conversation, nil)
		expectPlanned(t, applyPlan(t, conversation), plannedEntry{sections: pairs()})
		expectPlanned(t, applyPlan(t, conversation))
	})
}

type toolPlan struct {
	removed  []string
	added    []string
	sections [][2]any
}

func declaration(name string, description ...string) ai.ToolSchema {
	text := name
	if len(description) > 0 {
		text = description[0]
	}
	return ai.ToolSchema{Name: name, Description: text, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}

// applyTools plans tools only, appends the plan, checks that replay offers tools in order, and returns each message's changes.
func applyTools(t *testing.T, conversation Conversation, tools []ai.ToolSchema, sections ...[2]string) []toolPlan {
	t.Helper()
	drafts := PlanSystemEntries(contextOf(t, conversation), orderedOf(sections...), tools, 7)
	appendDrafts(t, conversation, drafts)
	offered := ai.GetCurrentTools(contextOf(t, conversation).Messages)
	want := []ai.ToolSchema{}
	for _, tool := range tools {
		want = append(want, ai.ToToolDeclaration(tool))
	}
	if len(offered) != len(want) {
		t.Fatalf("offered %d tools, want %d", len(offered), len(want))
	}
	for i := range offered {
		if !ai.DeclarationsEqual(offered[i], want[i]) || offered[i].Name != want[i].Name {
			t.Fatalf("offered %d is %s", i, offered[i].Name)
		}
	}
	plans := []toolPlan{}
	for _, draft := range drafts {
		message := draft.Model[0].(ai.SystemMessage)
		var plan toolPlan
		if message.ToolsRemoved != nil {
			plan.removed = []string{}
			for _, tool := range message.ToolsRemoved {
				plan.removed = append(plan.removed, tool.Name)
			}
		}
		if message.ToolsAdded != nil {
			plan.added = []string{}
			for _, tool := range message.ToolsAdded {
				plan.added = append(plan.added, tool.Name)
			}
		}
		if message.Sections != nil {
			plan.sections = sectionPairs(message.Sections)
		}
		plans = append(plans, plan)
	}
	return plans
}

func expectToolPlans(t *testing.T, got []toolPlan, want ...toolPlan) {
	t.Helper()
	if want == nil {
		want = []toolPlan{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestToolLoadoutPreparation(t *testing.T) {
	t.Run("adds, removes, replaces changed declarations, and rewrites the order when needed", func(t *testing.T) {
		conversation := promptRoot(t)
		a, b, c := declaration("a"), declaration("b"), declaration("c")
		tools := func(items ...ai.ToolSchema) []ai.ToolSchema { return items }
		expectToolPlans(t, applyTools(t, conversation, tools(a, b)), toolPlan{added: []string{"a", "b"}})
		expectToolPlans(t, applyTools(t, conversation, tools(a, b)))
		expectToolPlans(t, applyTools(t, conversation, tools(a, b, c)), toolPlan{added: []string{"c"}})
		expectToolPlans(t, applyTools(t, conversation, tools(a, c)), toolPlan{removed: []string{"b"}})
		// A changed declaration at the end is removed and re-added in place.
		c2 := declaration("c", "changed")
		expectToolPlans(t, applyTools(t, conversation, tools(a, c2)), toolPlan{removed: []string{"c"}, added: []string{"c"}})
		// A changed declaration in the middle would move to the end, so the whole order is rewritten.
		a2 := declaration("a", "changed")
		expectToolPlans(t, applyTools(t, conversation, tools(a2, c2)), toolPlan{removed: []string{"a", "c"}, added: []string{"a", "c"}})
		// Order-only change.
		expectToolPlans(t, applyTools(t, conversation, tools(c2, a2)), toolPlan{removed: []string{"a", "c"}, added: []string{"c", "a"}})
		expectToolPlans(t, applyTools(t, conversation, tools()), toolPlan{removed: []string{"c", "a"}})
	})

	t.Run("puts tool changes on the last section entry and re-declares every tool after a head cut", func(t *testing.T) {
		conversation := promptRoot(t)
		a, b := declaration("a"), declaration("b")
		expectToolPlans(t, applyTools(t, conversation, []ai.ToolSchema{a}, kv("x", "1"), kv("y", "2")),
			toolPlan{added: []string{"a"}, sections: pairs("x", "1", "y", "2")})
		// Section order changes need two entries; the tool change rides on the second.
		expectToolPlans(t, applyTools(t, conversation, []ai.ToolSchema{a, b}, kv("y", "2"), kv("x", "1")),
			toolPlan{sections: pairs("x", nil, "y", nil)},
			toolPlan{added: []string{"b"}, sections: pairs("y", "2", "x", "1")})
		marker(t, conversation, nil)
		expectToolPlans(t, applyTools(t, conversation, []ai.ToolSchema{a, b}, kv("y", "2"), kv("x", "1")),
			toolPlan{added: []string{"a", "b"}, sections: pairs("y", "2", "x", "1")})
	})
}
