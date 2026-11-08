package chat_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	draftscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/test/p2/chat-contracts.test.ts (abortCompaction, the owner's name), dequeue.test.ts and the second describe of
// drafts.test.ts, on the bare session host; the guests of dequeue.test.ts and drafts.test.ts are principals over memory links, as
// they are in Pi. The cases of chat-contracts.test.ts that take guests through share links (compact, reset and model metadata;
// dequeueOwn) are in apps/p2/e2e/chat_contracts_test.go, over real joins.

func hasEntry(kind string) func(delta.JsonValue) bool {
	return func(view delta.JsonValue) bool {
		for _, each := range list(obj(view).Value("entries")) {
			if obj(each).Value("kind") == kind {
				return true
			}
		}
		return false
	}
}

func docs(view delta.JsonValue, kind string) *delta.JsonObject {
	return obj(obj(obj(view).Value("docs")).Value(kind))
}

func running(view delta.JsonValue) bool { return docs(view, "pi.live").Value("run") != nil }

func queued(view delta.JsonValue) []*delta.JsonObject {
	var items []*delta.JsonObject
	for _, each := range list(docs(view, "pi.inbox").Value("items")) {
		items = append(items, obj(each))
	}
	return items
}

func queuedIDs(view delta.JsonValue) []any {
	ids := []any{}
	for _, item := range queued(view) {
		ids = append(ids, item.Value("id"))
	}
	return ids
}

func contains(view delta.JsonValue, id any) bool {
	for _, each := range queuedIDs(view) {
		if each == id {
			return true
		}
	}
	return false
}

func equalJSON(t *testing.T, got, want any) {
	t.Helper()
	if marshal(t, got) != marshal(t, want) {
		t.Errorf("got %s, want %s", marshal(t, got), marshal(t, want))
	}
}

func send(t *testing.T, chat *host.Reference, message string, requestID string, followUp bool) delta.JsonValue {
	t.Helper()
	args := delta.JsonObjectOf("text", message, "requestId", requestID)
	if followUp {
		args.Set("whenBusy", "followUp")
	}
	return mustCall(t, chat, "send", args)
}

func TestAbortCompactionAbortsARunningCompaction(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0))
	ownerChat := fixture.hold(t, owner, "participant")
	send(t, ownerChat, "first", "a", false)
	until(t, ownerChat.Member("view"), hasEntry("pi.assistant"))
	mustCall(t, ownerChat, "compact", delta.NewJsonObject(0))
	// The fake summary streams for a while: the abort marks the task before it places a summary.
	equalJSON(t, mustCall(t, ownerChat, "abortCompaction", nil), map[string]any{"aborted": 1})
	after := until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool {
		return len(list(docs(view, "pi.live").Value("compactions"))) == 0
	})
	if hasEntry("pi.compaction")(after) {
		t.Error("aborted: no summary")
	}
}

func TestOwnerNameTheOwnersNextEntriesCarryTheConfiguredNameEarlierOnesKeepTheirs(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0))
	ownerChat := fixture.hold(t, owner, "participant")
	authors := func(view delta.JsonValue) []string {
		var names []string
		for _, each := range list(obj(view).Value("entries")) {
			if entry := obj(each); entry.Value("kind") == "p2.input" {
				names = append(names, text(obj(entry.Value("data")).Value("author"), "name"))
			}
		}
		return names
	}
	send(t, ownerChat, "before", "before", false)
	until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool { return len(authors(view)) == 1 })
	// The host's own code changes the observed settings, as the models plugin's observer does.
	if _, err := fixture.Root(func(host.Ctx) (any, error) {
		return nil, fixture.settings.Replace(delta.JsonObjectOf("ownerName", "Mario"))
	}); err != nil {
		t.Fatal(err)
	}
	// Each send reads the name at that moment: the next one carries it.
	send(t, ownerChat, "after", "after", false)
	view := until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool { return len(authors(view)) == 2 })
	if got := authors(view); got[0] != "Owner" || got[1] != "Mario" {
		t.Errorf("authors = %v, want [Owner Mario]", got)
	}
}

func TestDequeueDiscardsAnotherAuthorsQueuedInputAndMoveBackReturnsItToItsAuthor(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0), drafts.Facet())
	ownerChat := fixture.hold(t, owner, "participant")
	guestChat := fixture.hold(t, alice, "participant")
	guestDrafts, err := p2test.HoldIn(fixture.Client(alice), draftscontract.Drafts, where, "author")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(guestDrafts.Release)
	send(t, ownerChat, "slow run", "run", false)
	until(t, ownerChat.Member("view"), running)
	removed := obj(send(t, guestChat, "removed by owner", "rm", true)).Value("submissionId")
	until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool { return contains(view, removed) })
	equalJSON(t, mustCall(t, ownerChat, "dequeue", delta.JsonObjectOf("submissionId", removed)), map[string]any{"text": nil})
	until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool { return !contains(view, removed) })
	pending, err := p2test.Settled(guestDrafts.Member("drafts"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, pending, []any{})

	moved := obj(send(t, guestChat, "moved by owner", "mv", true)).Value("submissionId")
	until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool { return contains(view, moved) })
	equalJSON(t, mustCall(t, ownerChat, "dequeue", delta.JsonObjectOf("submissionId", moved, "restore", true)), map[string]any{"text": nil})
	restored := list(until(t, guestDrafts.Member("drafts"), func(value delta.JsonValue) bool { return len(list(value)) > 0 }))
	if len(restored) != 1 || text(restored[0], "text") != "moved by owner" {
		t.Errorf("restored = %v", restored)
	}
	mustCall(t, ownerChat, "stop", nil)
}

// Drafts (owner, fake provider): a dequeued input goes back to its author once, and a removed one is discarded.
func TestDraftsReturnsADequeuedInputToItsAuthorOnceAndDiscardsARemovedOne(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0), drafts.Facet())
	chat := fixture.hold(t, owner, "participant")
	send(t, chat, "slow run", "run", false)
	until(t, chat.Member("view"), running)
	editable := obj(send(t, chat, "editable", "e", true)).Value("submissionId")
	removed := obj(send(t, chat, "removed", "r", true)).Value("submissionId")
	equalJSON(t, mustCall(t, chat, "dequeue", delta.JsonObjectOf("submissionId", editable, "restore", true)), map[string]any{"text": "editable"})
	// Already withdrawn input is never returned twice.
	equalJSON(t, mustCall(t, chat, "dequeue", delta.JsonObjectOf("submissionId", editable, "restore", true)), map[string]any{"text": nil})
	equalJSON(t, mustCall(t, chat, "dequeue", delta.JsonObjectOf("submissionId", removed)), map[string]any{"text": nil})
	send(t, chat, "steered", "s", false)
	send(t, chat, "followed", "f", true)
	stopped := mustCall(t, chat, "stop", nil)
	equalJSON(t, stopped, map[string]any{"restored": []string{"steered", "followed"}})
	until(t, chat.Member("view"), func(view delta.JsonValue) bool { return !running(view) })
}
