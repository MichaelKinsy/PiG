// Ports the guest cases of test/p2/chat-contracts.test.ts (Pi main c23b9ec1): "compact, reset and model metadata: owner, full
// guest and view guest" and "dequeueOwn restores only the caller's queued inputs, in order". Pi admits its guests through share
// links (`guestOf`): so do these, over a real server (POST /join, then a WebSocket with the guest cookie). The reset case's
// last assertion, on the web transcript (`buildTranscript`), belongs to the web lane.

package e2e_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// guestOf is a guest admitted through a share link of mode: the client of its cookie.
func guestOf(t *testing.T, fixture *p2test.Fixture, owner host.Host, session, mode, name string) host.Host {
	t.Helper()
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", mode), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool {
		for _, each := range list(value) {
			if object(each).Value("mode") == mode {
				return true
			}
		}
		return false
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var token any
	for _, each := range list(links) {
		if object(each).Value("mode") == mode {
			token = object(each).Value("token")
		}
	}
	body, _ := chordjson.Marshal(delta.JsonObjectOf("token", token, "name", name))
	headers := map[string]string{"Content-Type": "application/json"}
	for key, value := range tunnelHeaders {
		headers[key] = value
	}
	joined, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(string(body))})
	if err != nil || joined.Status != 200 {
		t.Fatalf("join: %v %+v", err, joined)
	}
	cookie, _, _ := strings.Cut(joined.Headers.Get("Set-Cookie"), ";")
	client, _ := fixture.Client(map[string]string{"Host": "share.test", "Origin": "https://share.test", "Cookie": cookie})
	return client
}

// sessionFixture is a fixture whose settings keep almost nothing verbatim, so a short conversation has something to compact,
// and a session of its owner.
func sessionFixture(t *testing.T) (fixture *p2test.Fixture, owner host.Host, session string, chat func(host.Host, string) *host.Reference) {
	t.Helper()
	fixture = p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	if err := os.WriteFile(filepath.Join(fixture.DataDir, "settings.json"), []byte(`{"compaction":{"keepRecentTokens":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	owner = fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session = object(created).Value("id").(string)
	chat = func(h host.Host, variant string) *host.Reference {
		t.Helper()
		ref, err := p2test.HoldIn(h, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), variant)
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	return fixture, owner, session, chat
}

func hasEntryKind(kind string) func(delta.JsonValue) bool {
	return func(view delta.JsonValue) bool {
		for _, each := range list(object(view).Value("entries")) {
			if object(each).Value("kind") == kind {
				return true
			}
		}
		return false
	}
}

func TestChatContractsCompactResetAndModelMetadataOwnerFullGuestAndViewGuest(t *testing.T) {
	fixture, owner, session, chat := sessionFixture(t)
	ownerChat := chat(owner, "participant")
	if _, err := ownerChat.Call("send", delta.JsonObjectOf("text", "first", "requestId", "a"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until := func(source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, 0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	until(ownerChat.Member("view"), hasEntryKind("pi.assistant"))
	// Guest-safe model metadata: the conversation's model from the catalog, also for a view-only guest.
	viewer := chat(guestOf(t, fixture, owner, session, "view", "Viewer"), "viewer")
	meta := object(until(viewer.Member("model"), func(value delta.JsonValue) bool { return value != nil }))
	if meta.Value("provider") != "faux" || meta.Value("modelId") != "faux-1" {
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
		_, err := viewer.Call(method, args, p2test.Background)
		p2test.ExpectChord(t, err, data.CodeInvalid)
	}

	// Compaction: a task, then a pi.compaction entry in the view; nothing to abort when idle.
	task, err := ownerChat.Call("compact", delta.JsonObjectOf("instructions", "keep it short"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := object(task).Value("taskId").(float64); id != float64(int(id)) {
		t.Errorf("taskId = %v", object(task).Value("taskId"))
	}
	until(ownerChat.Member("view"), hasEntryKind("pi.compaction"))
	aborted, err := ownerChat.Call("abortCompaction", nil, p2test.Background)
	if err != nil || object(aborted).Value("aborted") != float64(0) {
		t.Fatalf("nothing to abort when idle: %v %v", aborted, err)
	}

	// A full guest may compact and abort compaction, but not start a new context.
	guestChat := chat(guestOf(t, fixture, owner, session, "full", "Guest"), "participant")
	if aborted, err := guestChat.Call("abortCompaction", nil, p2test.Background); err != nil || object(aborted).Value("aborted") != float64(0) {
		t.Fatalf("guest abortCompaction: %v %v", aborted, err)
	}
	_, err = guestChat.Call("reset", delta.JsonObjectOf("handoff", "x"), p2test.Background)
	p2test.ExpectChord(t, err, data.CodeForbidden)

	// The owner's reset places a pi.reset entry carrying the handoff.
	if _, err := ownerChat.Call("reset", delta.JsonObjectOf("handoff", "Continue with the tests."), p2test.Background); err != nil {
		t.Fatal(err)
	}
	reset := until(ownerChat.Member("view"), hasEntryKind("pi.reset"))
	for _, each := range list(object(reset).Value("entries")) {
		if entry := object(each); entry.Value("kind") == "pi.reset" {
			model, _ := chordjson.Marshal(entry.Value("model"))
			if !strings.Contains(string(model), "Continue with the tests.") {
				t.Errorf("reset marker = %s", model)
			}
		}
	}
}

func TestChatContractsDequeueOwnRestoresOnlyTheCallersQueuedInputsInOrder(t *testing.T) {
	fixture, owner, session, chat := sessionFixture(t)
	ownerChat := chat(owner, "participant")
	guestChat := chat(guestOf(t, fixture, owner, session, "full", "Guest"), "participant")
	until := func(predicate func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(ownerChat.Member("view"), predicate, 0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	send := func(chat *host.Reference, text, id string, followUp bool) *delta.JsonObject {
		t.Helper()
		args := delta.JsonObjectOf("text", text, "requestId", id)
		if followUp {
			args.Set("whenBusy", "followUp")
		}
		result, err := chat.Call("send", args, p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(result)
	}
	queued := func(view delta.JsonValue) []any {
		return list(object(object(object(view).Value("docs")).Value("pi.inbox")).Value("items"))
	}
	send(ownerChat, "slow run", "run", false)
	until(func(view delta.JsonValue) bool { return live(view) != nil && live(view).Value("run") != nil })
	one := send(ownerChat, "owner one", "o1", true)
	theirs := send(guestChat, "guest one", "g1", true)
	two := send(ownerChat, "owner two", "o2", true)
	until(func(view delta.JsonValue) bool { return len(queued(view)) == 3 })
	restored, err := ownerChat.Call("dequeueOwn", nil, p2test.Background)
	if err != nil || !jsonEqual(restored, map[string]any{"restored": []any{"owner one", "owner two"}, "skipped": float64(0)}) {
		t.Fatalf("dequeueOwn = %v %v", restored, err)
	}
	after := until(func(view delta.JsonValue) bool { return len(queued(view)) == 1 })
	if got := object(queued(after)[0]).Value("id"); got != theirs.Value("submissionId") {
		t.Errorf("queued = %v, want the guest's %v", got, theirs.Value("submissionId"))
	}
	if one.Value("submissionId").(float64) >= two.Value("submissionId").(float64) {
		t.Error("submissions must ascend")
	}
	// The guest's own call returns theirs; nothing of the owner's is left.
	for who, call := range map[string]*host.Reference{"guest": guestChat, "owner": ownerChat} {
		want := map[string]any{"restored": []any{}, "skipped": float64(0)}
		if who == "guest" {
			want["restored"] = []any{"guest one"}
		}
		got, err := call.Call("dequeueOwn", nil, p2test.Background)
		if err != nil || !jsonEqual(got, want) {
			t.Errorf("%s dequeueOwn = %v %v", who, got, err)
		}
	}
	if _, err := ownerChat.Call("stop", nil, p2test.Background); err != nil {
		t.Fatal(err)
	}
}
