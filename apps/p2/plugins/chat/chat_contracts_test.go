package chat_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	draftscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/test/p2/chat-contracts.test.ts (the Chat contracts: compact, abortCompaction, reset, dequeueOwn, the
// guest-safe model metadata, the owner's name), dequeue.test.ts and the second describe of drafts.test.ts, on the bare
// session host. Owner, full guest and view guest are principals over memory links, where Pi joins by share links.

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

func TestCompactResetAndModelMetadataOwnerFullGuestAndViewGuest(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0))
	ownerChat := fixture.hold(t, owner, "participant")
	send(t, ownerChat, "first", "a", false)
	until(t, ownerChat.Member("view"), hasEntry("pi.assistant"))
	// Guest-safe model metadata: the conversation's model from the catalog, also for a view-only guest.
	viewer := fixture.hold(t, bob, "viewer")
	meta := obj(until(t, viewer.Member("model"), func(value delta.JsonValue) bool { return value != nil }))
	if text(meta, "provider") != "faux" || text(meta, "modelId") != "faux-1" {
		t.Errorf("meta = %v", meta)
	}
	if window, _ := meta.Value("contextWindow").(float64); window <= 0 {
		t.Errorf("contextWindow = %v", meta.Value("contextWindow"))
	}
	if _, ok := meta.Value("thinkingLevels").([]any); !ok {
		t.Errorf("thinkingLevels = %v", meta.Value("thinkingLevels"))
	}
	// A reference carries every variant's members; the viewer's binding refuses the participant's.
	for method, args := range map[string]delta.JsonValue{"compact": delta.NewJsonObject(0), "reset": delta.NewJsonObject(0), "dequeueOwn": nil} {
		_, err := call(viewer, method, args)
		p2test.ExpectChord(t, err, data.CodeInvalid)
	}

	// Compaction: a task, then a pi.compaction entry in the view; nothing to abort when idle.
	task := mustCall(t, ownerChat, "compact", delta.JsonObjectOf("instructions", "keep it short"))
	if id, _ := obj(task).Value("taskId").(float64); id != float64(int(id)) {
		t.Errorf("taskId = %v", obj(task).Value("taskId"))
	}
	until(t, ownerChat.Member("view"), hasEntry("pi.compaction"))
	equalJSON(t, mustCall(t, ownerChat, "abortCompaction", nil), map[string]any{"aborted": 0})

	// A full guest may compact and abort compaction, but not start a new context.
	guestChat := fixture.hold(t, alice, "participant")
	equalJSON(t, mustCall(t, guestChat, "abortCompaction", nil), map[string]any{"aborted": 0})
	_, err := call(guestChat, "reset", delta.JsonObjectOf("handoff", "x"))
	p2test.ExpectChord(t, err, data.CodeForbidden)

	// The owner's reset places a pi.reset entry carrying the handoff.
	mustCall(t, ownerChat, "reset", delta.JsonObjectOf("handoff", "Continue with the tests."))
	reset := until(t, ownerChat.Member("view"), hasEntry("pi.reset"))
	for _, each := range list(obj(reset).Value("entries")) {
		if entry := obj(each); entry.Value("kind") == "pi.reset" && !strings.Contains(marshal(t, entry.Value("model")), "Continue with the tests.") {
			t.Errorf("reset marker = %v", entry)
		}
	}
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

func TestDequeueOwnRestoresOnlyTheCallersQueuedInputsInOrder(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0))
	ownerChat := fixture.hold(t, owner, "participant")
	guestChat := fixture.hold(t, alice, "participant")
	send(t, ownerChat, "slow run", "run", false)
	until(t, ownerChat.Member("view"), running)
	one := send(t, ownerChat, "owner one", "o1", true)
	theirs := send(t, guestChat, "guest one", "g1", true)
	two := send(t, ownerChat, "owner two", "o2", true)
	until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool { return len(queued(view)) == 3 })
	equalJSON(t, mustCall(t, ownerChat, "dequeueOwn", nil), map[string]any{"restored": []string{"owner one", "owner two"}, "skipped": 0})
	after := until(t, ownerChat.Member("view"), func(view delta.JsonValue) bool { return len(queued(view)) == 1 })
	equalJSON(t, queuedIDs(after), []any{obj(theirs).Value("submissionId")})
	if obj(one).Value("submissionId").(float64) >= obj(two).Value("submissionId").(float64) {
		t.Error("submissions must ascend")
	}
	// The guest's own call returns theirs; nothing of the owner's is left.
	equalJSON(t, mustCall(t, guestChat, "dequeueOwn", nil), map[string]any{"restored": []string{"guest one"}, "skipped": 0})
	equalJSON(t, mustCall(t, ownerChat, "dequeueOwn", nil), map[string]any{"restored": []string{}, "skipped": 0})
	mustCall(t, ownerChat, "stop", nil)
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
