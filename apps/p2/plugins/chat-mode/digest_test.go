package chatmode

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports the pure cases of test/p2/chat-mode.test.ts (Pi main c23b9ec1): the mention rules are in contract_test.go; here the
// digest, the mention's composition, what each chat message is passed on with, and the document's migration.

var (
	owner = p2runtime.Author{Kind: p2runtime.AuthorOwner, ID: "owner", Name: "Owner"}
	ann   = p2runtime.Author{Kind: p2runtime.AuthorGuest, ID: "ann", Name: "Ann"}
	bob   = p2runtime.Author{Kind: p2runtime.AuthorGuest, ID: "bob", Name: "Bob"}
)

func matches(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("%q does not match %s", text, pattern)
	}
}

func TestDigestNamesWhoAMessageAnswersAndQuotesTheStartOfIt(t *testing.T) {
	chat := []ChatEntryRef{
		{ID: 7, Message: ChatMessage{Author: ann, Text: "should we split the tokenizer and the parser work?"}},
		{ID: 9, Message: ChatMessage{
			Author: bob, Text: "yes, the tokenizer part",
			ReplyTo: &ReplyTo{EntryID: 7, Author: ReplyAuthor{Kind: "guest", ID: "ann", Name: "Ann"}, Excerpt: "should we split…"},
		}},
	}
	text := Digest(chat, owner, "@pi agreed?", nil)
	matches(t, `(?m)^Bob \(guest, replying to Ann: "should we split…"\): yes, the tokenizer part$`, text)
	matches(t, `\nOwner mentioned you:\n@pi agreed\?$`, text)
}

func TestComposesAMentionInOnePlace(t *testing.T) {
	file := delta.JsonObjectOf("id", "u1", "name", "plan.md", "path", "/uploads/u1-plan.md", "size", float64(3))
	chat := []ChatEntryRef{{ID: 4, Message: ChatMessage{Author: ann, Text: "lunch?"}}}
	submission := ComposeMention(Mention{
		Author: owner, Text: "@pi go", Chat: chat, Attachments: []File{{ID: "u1", Name: "plan.md"}}, Files: []any{file},
		RequestID: "r", WhenBusy: "followUp",
	})
	if submission.EntryKind != "chat.mention" {
		t.Fatalf("kind %q", submission.EntryKind)
	}
	encoded, _ := json.Marshal(submission.EntryData)
	want := `{"author":{"kind":"owner","id":"owner","name":"Owner"},"text":"@pi go","attachments":[{"id":"u1","name":"plan.md"}],"included":[4]}`
	var got, expected any
	_ = json.Unmarshal(encoded, &got)
	_ = json.Unmarshal([]byte(want), &expected)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("data %s, want %s", encoded, want)
	}
	matches(t, `^<chat>\n[^\n]*\nAnn \(guest\): lunch\?\n</chat>\n\nOwner mentioned you:\n@pi go$`, submission.Text)
	// The chat plugin writes the author's speaker block for the model.
	if submission.Author != owner || submission.MaxImages != MaxDigestImages || submission.WhenBusy != "followUp" || submission.RequestID != "r" {
		t.Fatalf("submission %+v", submission)
	}
}

func chatEntry(id durable.EntryId, text string) durable.EntryRecord {
	return durable.EntryRecord{Id: id, Kind: "chat.message", Data: delta.JsonObjectOf("author", authorJSON(ann), "text", text, "attachments", []any{})}
}

func mentionEntry(id durable.EntryId, included ...float64) durable.EntryRecord {
	list := make([]any, len(included))
	for i, each := range included {
		list[i] = each
	}
	return durable.EntryRecord{
		Id: id, Kind: "chat.mention",
		Data:  delta.JsonObjectOf("author", authorJSON(ann), "text", "@pi", "attachments", []any{}, "included", list),
		Model: []ai.Message{ai.UserMessage{Content: ai.UserText("digest")}},
	}
}

func idsOf(refs []ChatEntryRef) []float64 {
	out := []float64{}
	for _, ref := range refs {
		out = append(out, ref.ID)
	}
	return out
}

func TestPassesOnEachChatMessageOnce(t *testing.T) {
	// Mention 5 was composed with chat 2 and 3; chat 4 was placed while it waited in the queue.
	entries := []durable.EntryRecord{
		chatEntry(6, "after"), mentionEntry(5, 2, 3), chatEntry(4, "raced"), chatEntry(3, "b"), chatEntry(2, "a"), chatEntry(1, "old"),
	}
	if got := idsOf(Unmentioned(entries, nil)); !slices.Equal(got, []float64{4, 6}) {
		t.Fatalf("got %v, want [4 6]", got)
	}
	// A mention still queued covers its chat too.
	inbox := &harness.InboxState{Items: []harness.InboxItem{{
		Id: 99, Mode: harness.InboxFollowUp, Content: "digest",
		Entry: delta.JsonObjectOf("kind", "chat.mention", "data", delta.JsonObjectOf(
			"author", authorJSON(ann), "text", "@pi", "attachments", []any{}, "included", []any{float64(4), float64(6)})),
	}}}
	if got := Unmentioned(entries, inbox); len(got) != 0 {
		t.Fatalf("got %v, want none", idsOf(got))
	}
	// The scan stops at the lowest chat entry the latest mention passed on.
	if !ReadEnough(entries) {
		t.Fatal("a read that reached the mention's lowest chat entry is enough")
	}
	if ReadEnough(entries[:3]) {
		t.Fatal("a read that stopped above the mention's lowest chat entry is not enough")
	}
	// A mention that passed on nothing bounds nothing.
	if ReadEnough([]durable.EntryRecord{chatEntry(6, "x"), mentionEntry(5)}) {
		t.Fatal("a mention that passed nothing on bounds nothing")
	}
}

func TestChatModeDocMigratesVersionOneToTheDefaultName(t *testing.T) {
	migrated, err := ChatModeDoc.AnyDefinition().Migrate(delta.JsonObjectOf("enabled", true), 1)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Value("enabled") != true || migrated.Value("name") != "Pi" {
		t.Fatalf("migrated %v", migrated)
	}
}

func TestExcerptCollapsesWhitespaceAndCutsWithAnEllipsis(t *testing.T) {
	if got := Excerpt("  a \n\t b  "); got != "a b" {
		t.Fatalf("got %q", got)
	}
	long := "should we split the tokenizer work from the parser work, or keep both in one change?"
	if got := Excerpt(long); got != "should we split the tokenizer work from the parser work, or…" {
		t.Fatalf("got %q", got)
	}
}
