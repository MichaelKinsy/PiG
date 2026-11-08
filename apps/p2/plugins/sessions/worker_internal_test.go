package sessions

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func appendNotes(t *testing.T, opened harness.Harness, notes ...string) []durable.EntryId {
	t.Helper()
	root, err := opened.Root(p2test.Background, &harness.RootOptions{})
	if err != nil || root == nil {
		t.Fatalf("root: %v", err)
	}
	var ids []durable.EntryId
	if _, err := opened.Commit(p2test.Background, func(tx durable.Tx) (any, error) {
		for _, note := range notes {
			entry, err := tx.AppendEntry(durable.ROOT_CONVERSATION_ID, durable.EntryDraft{Kind: "note", Data: delta.JsonObjectOf("text", note)})
			if err != nil {
				return nil, err
			}
			ids = append(ids, entry.Id)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func marshalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestHistoryIsTheContextBeforeTheChosenEntryAndSeedsANewSession(t *testing.T) {
	source := p2test.OpenHarness(t)
	ids := appendNotes(t, source, "one", "two", "three")
	ctx := context.Background()

	out, err := history(ctx, source, durable.ROOT_CONVERSATION_ID, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	entries := out.(*delta.JsonObject).Value("entries").([]any)
	var texts []string
	for _, entry := range entries {
		data, _ := entry.(*delta.JsonObject).Value("data").(*delta.JsonObject)
		if data != nil {
			texts = append(texts, data.Value("text").(string))
		}
	}
	if strings.Join(texts, ",") != "one,two" {
		t.Fatalf("history = %s", marshalJSON(t, out))
	}

	// A fork of the first message holds nothing.
	first, err := history(ctx, source, durable.ROOT_CONVERSATION_ID, ids[0])
	if err != nil || len(first.(*delta.JsonObject).Value("entries").([]any)) != 0 {
		t.Fatalf("history of the first entry = %s, %v", marshalJSON(t, first), err)
	}
	if _, err := history(ctx, source, durable.ROOT_CONVERSATION_ID, ids[2]+100); err == nil || err.Error() != "Unknown message" {
		t.Fatalf("an unknown message: %v", err)
	}
	if _, err := history(ctx, source, 99, ids[0]); err == nil || err.Error() != "Forking a subagent conversation is not supported yet" {
		t.Fatalf("an unknown conversation is no top-level one: %v", err)
	}

	// Seeding appends the drafts to a fresh session's main conversation, once.
	fork := p2test.OpenHarness(t)
	if _, err := fork.Root(ctx, &harness.RootOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := seed(ctx, fork, entries); err != nil {
		t.Fatal(err)
	}
	root, _ := fork.Conversation(ctx, durable.ROOT_CONVERSATION_ID)
	seeded, err := root.Entries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, nil)
	if err != nil || len(seeded.Items) != len(entries) {
		t.Fatalf("seeded %d entries, want %d (%v)", len(seeded.Items), len(entries), err)
	}
	if err := seed(ctx, fork, entries); err == nil || err.Error() != "The session has history already" {
		t.Fatalf("a second seed: %v", err)
	}
}

func TestHistoryOfASubagentIsNotSupportedYet(t *testing.T) {
	opened := p2test.OpenHarness(t)
	appendNotes(t, opened, "one")
	child, err := opened.CreateConversation(p2test.Background, harness.ConversationCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history(p2test.Background, opened, child.Id(), 1); err == nil || err.Error() != "Forking a subagent conversation is not supported yet" {
		t.Fatalf("subagent history: %v", err)
	}
}

func TestCleanTitleCollapsesWhiteSpaceTheWayJavaScriptDoes(t *testing.T) {
	for in, want := range map[string]string{
		"  a  b\t\nc  ": "a b c", "": "", " \u00a0 ": "", "\ufeffx\u2003y": "x y", "no-change": "no-change",
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzCleanTitle(f *testing.F) {
	for _, seed := range []string{"", "a  b", "\t x\n", "\ufeff", "é\u00a0é"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := cleanTitle(in)
		if got != cleanTitle(got) {
			t.Fatalf("not idempotent: %q -> %q -> %q", in, got, cleanTitle(got))
		}
		if strings.Contains(got, "  ") || strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
			t.Fatalf("white space left in %q", got)
		}
	})
}
