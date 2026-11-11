package e2e_test

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views"
	viewscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/viewstest"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// A script that has the agent call view `room`'s method.
func callScript(method, args string) string {
	return `codemode return JSON.stringify(await tools.view_call({ view: "room", method: "` + method + `", args: ` + args + ` }).catch((error) => "failed: " + error.message))`
}

// tryPrompt is prompt for a goroutine: the result text, or why there is none.
func (f *roomsFixture) tryPrompt(chat *host.Reference, tool, line, id string) (string, error) {
	current, _ := chat.Member("view").Value()
	before := len(toolResults(current, tool))
	if _, err := chat.Call("send", delta.JsonObjectOf("text", line, "requestId", id), p2test.Background); err != nil {
		return "", err
	}
	view, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool { return len(toolResults(value, tool)) == before+1 && idle(value) }, 0)
	if err != nil {
		return "", err
	}
	results := toolResults(view, tool)
	return toolText(results[len(results)-1]), nil
}

// answerCalls answers each call given to peer through ref with answer (a frame's methods); stop ends it.
func answerCalls(t *testing.T, ref *host.Reference, peer string, answer func(call *delta.JsonObject) delta.JsonValue) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	var wg sync.WaitGroup
	answered := map[string]bool{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			value, _ := ref.Member("room").Value()
			for _, each := range list(object(value).Value("calls")) {
				call := object(each)
				id, _ := call.Value("id").(string)
				if call.Value("peer") == peer && !answered[id] {
					answered[id] = true
					_, _ = ref.Call("answer", delta.JsonObjectOf("id", id, "result", answer(call)), p2test.Background)
				}
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

func loadTextOf(t *testing.T, room *delta.JsonObject) *views.TextDoc {
	t.Helper()
	base := object(room.Value("base"))
	if base == nil {
		t.Fatal("the room has no base")
	}
	doc, _, ok := views.LoadText(base.Value("value"))
	if !ok {
		t.Fatal("the base holds no valid text")
	}
	return doc
}

func editCall(ref *host.Reference, batch *viewstest.Batch) (*host.PendingCall, error) {
	return ref.Start("edit", delta.JsonObjectOf("n", float64(batch.N), "edits", viewstest.EditsValue(batch.Edits)), p2test.Background)
}

func TestViewRoomsTextCallsAndReloads(t *testing.T) {
	var rebuilds p2test.Rebuilds
	f := newRoomsFixture(t, hosts.ServerOptions{BuildRevision: rebuilds.Revision})
	roomCall := func(ref *host.Reference, name string, args *delta.JsonObject) *delta.JsonObject {
		return call(t, ref, name, args)
	}
	join := func(ref *host.Reference, kind string) *delta.JsonObject {
		args := delta.NewJsonObject(0)
		if kind != "" {
			args.Set("kind", kind)
		}
		return roomCall(ref, "join", args)
	}
	textOf := func(value delta.JsonValue) string {
		text, _ := object(value).Value("text").(string)
		return text
	}
	holdState := func(session string) *host.Reference {
		state, err := p2test.HoldIn(f.owner, viewscontract.ViewState, delta.JsonObjectOf("session", session, "view", "room"), "editor")
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	untilText := func(state *host.Reference, want string) {
		t.Helper()
		if _, err := p2test.Until(state.Member("value"), func(value delta.JsonValue) bool { return textOf(value) == want }, 0); err != nil {
			t.Fatalf("waiting for the state's text %.30q: %v", want, err)
		}
	}

	t.Run("converges two editors of a text room, and takes the agent's text as an edit", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		a := f.crafted(s.id, opening, "a")
		b := f.crafted(s.id, opening, "b")
		joined := join(a, "text")
		join(b, "text")
		doc := loadTextOf(t, untilMember(t, a, "room", func(value *delta.JsonObject) bool { return value.Value("base") != nil }))
		left := viewstest.NewTextClient("editor-a", doc, 0)
		right := viewstest.NewTextClient("editor-b", doc, 0)
		if joined.Value("editor") != "editor-a" {
			t.Fatalf("editor = %v", joined.Value("editor"))
		}
		// "hello" from both at once, then a delete over b's insert.
		one, two := left.Input("hello"), right.Input("HELLO")
		if one == nil || two == nil {
			t.Fatal("no batches")
		}
		sendBoth := func(first *host.Reference, firstBatch *viewstest.Batch, second *host.Reference, secondBatch *viewstest.Batch) {
			t.Helper()
			started := []*host.PendingCall{}
			for i, each := range []struct {
				ref   *host.Reference
				batch *viewstest.Batch
			}{{first, firstBatch}, {second, secondBatch}} {
				pending, err := editCall(each.ref, each.batch)
				if err != nil {
					t.Fatalf("edit %d: %v", i, err)
				}
				started = append(started, pending)
			}
			for _, pending := range started {
				if _, err := pending.Wait(p2test.Background); err != nil {
					t.Fatal(err)
				}
			}
		}
		sendBoth(a, one, b, two)
		apply := func(log *delta.JsonObject, after int) {
			for _, each := range list(log.Value("messages")) {
				message := object(each)
				if intOfValue(message.Value("seq")) > after && message.Has("n") {
					from, _ := message.Value("from").(string)
					edits := viewstest.EditsOfLog(message.Value("data").(string))
					left.Confirm(from, intOfValue(message.Value("n")), edits)
					right.Confirm(from, intOfValue(message.Value("n")), edits)
				}
			}
		}
		apply(untilMember(t, a, "log", func(value *delta.JsonObject) bool { return len(list(value.Value("messages"))) == 2 }), 0)
		if left.Doc().String() != right.Doc().String() {
			t.Fatalf("editors differ: %q and %q", left.Doc().String(), right.Doc().String())
		}
		merged := left.Doc().String()
		if merged != "helloHELLO" && merged != "HELLOhello" {
			t.Fatal(merged)
		}
		cut, add := left.Input(""), right.Input(merged+"!")
		if cut == nil || add == nil {
			t.Fatal("no batches")
		}
		sendBoth(b, add, a, cut)
		apply(untilMember(t, a, "log", func(value *delta.JsonObject) bool { return len(list(value.Value("messages"))) == 4 }), 2)
		if left.Doc().String() != right.Doc().String() {
			t.Fatalf("editors differ: %q and %q", left.Doc().String(), right.Doc().String())
		}
		// A batch sent again (a reconnect) is a no-op: its number is at or below the editor's watermark.
		roomCall(a, "edit", delta.JsonObjectOf("n", float64(one.N), "edits", viewstest.EditsValue(one.Edits)))
		echoed := untilMember(t, a, "log", func(value *delta.JsonObject) bool { return len(list(value.Value("messages"))) == 5 })
		if last := object(list(echoed.Value("messages"))[4]); last.Value("data") != "[]" {
			t.Errorf("the repeated batch was logged as %v", last.Value("data"))
		}
		// The view's state gets the text; the agent's text becomes one edit.
		state := holdState(s.id)
		defer state.Release()
		untilText(state, left.Doc().String())
		f.prompt(s.chat, "view", "view "+viewsJSON(t, map[string]any{"action": "update", "id": "room", "merge": map[string]any{"text": "from the agent"}}))
		agent := untilMember(t, a, "log", func(value *delta.JsonObject) bool {
			for _, each := range list(value.Value("messages")) {
				if object(each).Value("from") == "server" && object(each).Has("n") {
					return true
				}
			}
			return false
		})
		apply(agent, 5)
		if left.Doc().String() != "from the agent" {
			t.Errorf("the agent's text: %q", left.Doc().String())
		}
		// A page's text over the limit is not taken: the room's text is written back.
		if _, err := state.Call("set", delta.JsonObjectOf("value", delta.JsonObjectOf("text", strings.Repeat("x", 100_001))), p2test.Background); err != nil {
			t.Fatal(err)
		}
		untilText(state, "from the agent")
	})

	t.Run("runs the agent's call in the leader's frame, and fails at once without one", func(t *testing.T) {
		s := f.session()
		if text := f.prompt(s.chat, "codemode", callScript("zoom", "{ x: 2 }")); !regexp.MustCompile(`not open`).MatchString(text) {
			t.Fatalf("a call without a frame: %s", text)
		}
		_, opening, _ := f.token(s.id, "room")
		leader := f.crafted(s.id, opening, "leader")
		roomCall(leader, "offer", delta.JsonObjectOf("methods", []any{delta.JsonObjectOf("name", "zoom", "description", "Zooms",
			"args", delta.JsonObjectOf("type", "object", "properties", delta.JsonObjectOf("x", delta.JsonObjectOf("type", "number")), "required", []any{"x"}))}))
		// The frame answers each call given to it.
		stop := answerCalls(t, leader, "leader", func(call *delta.JsonObject) delta.JsonValue {
			x, _ := object(call.Value("args")).Value("x").(float64)
			return delta.JsonObjectOf("zoomed", x*10)
		})
		defer stop()
		if text := f.prompt(s.chat, "codemode", callScript("zoom", "{ x: 2 }")); !regexp.MustCompile(`zoomed.*20`).MatchString(text) {
			t.Fatalf("the frame's answer: %s", text)
		}
	})

	zoom := delta.JsonObjectOf("methods", []any{delta.JsonObjectOf("name", "zoom", "description", "Zooms", "args", delta.JsonObjectOf("type", "object"))})
	asked := func(s roomSession) chan string {
		out := make(chan string, 1)
		go func() {
			text, err := f.tryPrompt(s.chat, "codemode", callScript("zoom", "{}"), "rooms-async-"+s.id)
			if err != nil {
				text = "error: " + err.Error()
			}
			out <- text
		}()
		return out
	}
	hasCallFor := func(peer string) func(*delta.JsonObject) bool {
		return func(value *delta.JsonObject) bool {
			for _, each := range list(value.Value("calls")) {
				if object(each).Value("peer") == peer {
					return true
				}
			}
			return false
		}
	}

	t.Run("fails a call as uncertain when its frame leaves before answering", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		leader := f.crafted(s.id, opening, "leader")
		next := f.crafted(s.id, opening, "next")
		roomCall(leader, "offer", zoom)
		roomCall(next, "offer", zoom)
		text := asked(s)
		untilMember(t, leader, "room", hasCallFor("leader"))
		leader.Release()
		if got := <-text; !strings.Contains(got, "may or may not have run") {
			t.Fatalf("the call: %s", got)
		}
		// Not given to the next leader: it was not offered as idempotent.
		if calls := list(member(next, "room").Value("calls")); len(calls) != 0 {
			t.Errorf("calls = %v", calls)
		}
	})

	t.Run("fails a call as uncertain when its frame, the last one, leaves before answering", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		leader := f.crafted(s.id, opening, "leader")
		roomCall(leader, "offer", zoom)
		text := asked(s)
		untilMember(t, leader, "room", hasCallFor("leader"))
		leader.Release()
		if got := <-text; !strings.Contains(got, "may or may not have run") {
			t.Fatalf("the call: %s", got)
		}
	})

	t.Run("takes a snapshot that is overtaken by a newer one as stale", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		leader := f.crafted(s.id, opening, "leader")
		join(leader, "log")
		roomCall(leader, "send", send(map[string]any{"data": "1"}, map[string]any{"data": "2"}))
		room := untilMember(t, leader, "room", func(value *delta.JsonObject) bool { return intOfValue(value.Value("seq")) == 2 })
		base := object(room.Value("base"))
		snapshot := func(seq float64) *delta.JsonObject {
			return delta.JsonObjectOf("seq", seq, "epoch", base.Value("epoch"), "term", room.Value("term"), "value", delta.JsonObjectOf("upTo", seq))
		}
		// Sent together: the newer goes first, the older one after it must not move the base back.
		first, err := leader.Start("snapshot", snapshot(2), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		second, err := leader.Start("snapshot", snapshot(1), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		_, firstErr := first.Wait(p2test.Background)
		_, secondErr := second.Wait(p2test.Background)
		if firstErr != nil || secondErr == nil {
			t.Errorf("snapshots: %v and %v, want written and stale", firstErr, secondErr)
		}
		if seq := intOfValue(object(member(leader, "room").Value("base")).Value("seq")); seq != 2 {
			t.Errorf("the base is at %d, want 2", seq)
		}
	})

	t.Run("ends a call its caller cancels, and takes it out of the room", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		leader := f.crafted(s.id, opening, "leader")
		roomCall(leader, "offer", delta.JsonObjectOf("methods", []any{delta.JsonObjectOf("name", "slow", "description", "Never answers", "args", delta.JsonObjectOf("type", "object"))}))
		worker, err := p2test.HoldIn(f.fixture.As(p2runtime.WorkerPrincipal{Session: s.id, Generation: 1}), viewscontract.ViewRoom, peerParams(s.id, opening), "worker")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := chordctx.WithCancel(p2test.Background)
		result := make(chan error, 1)
		go func() {
			_, err := worker.Call("call", delta.JsonObjectOf("method", "slow", "args", delta.NewJsonObject(0)), ctx)
			result <- err
		}()
		untilMember(t, leader, "room", func(value *delta.JsonObject) bool { return len(list(value.Value("calls"))) == 1 })
		cancel(errors.New("cancelled by the test"))
		if err := <-result; err == nil {
			t.Error("the cancelled call succeeded")
		}
		untilMember(t, leader, "room", func(value *delta.JsonObject) bool { return len(list(value.Value("calls"))) == 0 })
	})

	t.Run("writes a text room's text when the plugin reloads before its save", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		editor := f.crafted(s.id, opening, "editor")
		join(editor, "text")
		doc := loadTextOf(t, untilMember(t, editor, "room", func(value *delta.JsonObject) bool { return value.Value("base") != nil }))
		batch := viewstest.NewTextClient("editor-editor", doc, 0).Input("kept across a reload")
		roomCall(editor, "edit", delta.JsonObjectOf("n", float64(batch.N), "edits", viewstest.EditsValue(batch.Edits)))
		rebuilds.Rebuild("views")
		if _, err := f.fixture.Server.Reload([]string{"views"}, p2test.Background); err != nil {
			t.Fatal(err)
		}
		state := holdState(s.id)
		defer state.Release()
		untilText(state, "kept across a reload")
	})

	t.Run("takes the agent's change of the view's state made while the plugin reloads", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		leader := f.crafted(s.id, opening, "leader")
		join(leader, "log")
		roomCall(leader, "send", send(map[string]any{"data": "1"}))
		before := object(untilMember(t, leader, "room", func(value *delta.JsonObject) bool { return value.Value("base") != nil }).Value("base"))
		rebuilds.Rebuild("views")
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.fixture.Server.Reload([]string{"views"}, p2test.Background); err != nil {
				t.Error(err)
			}
		}()
		text, err := f.tryPrompt(s.chat, "view", "view "+viewsJSON(t, map[string]any{"action": "update", "id": "room", "state": map[string]any{"reset": "by the agent"}}), "rooms-reload-"+s.id)
		if err != nil {
			t.Fatal(text, err)
		}
		wg.Wait()
		after := untilMember(t, leader, "room", func(value *delta.JsonObject) bool {
			return object(value.Value("base")) != nil && viewsJSON(t, object(value.Value("base")).Value("value")) == `{"reset":"by the agent"}`
		})
		if intOfValue(object(after.Value("base")).Value("epoch")) <= intOfValue(before.Value("epoch")) {
			t.Error("the agent's reset did not move the epoch")
		}
	})

	t.Run("takes the view's text changed while no room was open", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		first := f.crafted(s.id, opening, "first")
		join(first, "text")
		doc := loadTextOf(t, untilMember(t, first, "room", func(value *delta.JsonObject) bool { return value.Value("base") != nil }))
		batch := viewstest.NewTextClient("editor-first", doc, 0).Input("A")
		roomCall(first, "edit", delta.JsonObjectOf("n", float64(batch.N), "edits", viewstest.EditsValue(batch.Edits)))
		state := holdState(s.id)
		defer state.Release()
		untilText(state, "A")
		// The last frame leaves and the room ends after its linger; then the agent writes the text.
		first.Release()
		time.Sleep(11 * time.Second)
		f.prompt(s.chat, "view", "view "+viewsJSON(t, map[string]any{"action": "update", "id": "room", "merge": map[string]any{"text": "B"}}))
		untilText(state, "B")
		next := f.crafted(s.id, opening, "next")
		join(next, "text")
		reopened := loadTextOf(t, untilMember(t, next, "room", func(value *delta.JsonObject) bool { return value.Value("base") != nil }))
		if reopened.String() != "B" {
			t.Fatalf("the reopened room's text is %q", reopened.String())
		}
		// Still B after the room's next write (an edit of its own would have written A back before the fix).
		edit := viewstest.NewTextClient("editor-next", reopened, 0).Input("B!")
		roomCall(next, "edit", delta.JsonObjectOf("n", float64(edit.N), "edits", viewstest.EditsValue(edit.Edits)))
		untilText(state, "B!")
	})

	t.Run("lets a frame that only offers methods answer calls", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		frame := f.crafted(s.id, opening, "only")
		// No room asked for: joining without a kind gives the frame its peer ID and keeps the room's kind open.
		if me := join(frame, "").Value("me"); me != "only" {
			t.Fatalf("me = %v", me)
		}
		if kind := member(frame, "room").Value("kind"); kind != nil {
			t.Fatalf("the room's kind = %v", kind)
		}
		roomCall(frame, "offer", delta.JsonObjectOf("methods", []any{delta.JsonObjectOf("name", "ping", "description", "Pings", "args", delta.JsonObjectOf("type", "object"))}))
		stop := answerCalls(t, frame, "only", func(*delta.JsonObject) delta.JsonValue { return "pong" })
		defer stop()
		if text := f.prompt(s.chat, "codemode", `codemode return JSON.stringify(await tools.view_call({ view: "room", method: "ping", args: {} }))`); !strings.Contains(text, "pong") {
			t.Fatalf("the frame's answer: %s", text)
		}
	})
}

// A memory budget small from the start: each case its own server, as a room keeps what it was charged until it ends, which would
// spend the next case's budget.
func TestViewRoomsMemoryBudget(t *testing.T) {
	start := func(t *testing.T) (*roomsFixture, roomSession, string, *host.Reference) {
		t.Helper()
		f := newRoomsFixture(t, hosts.ServerOptions{})
		settings := f.fixture.DataDir + "/settings.json"
		if err := os.WriteFile(settings, []byte(`{"viewRoomsMemory":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		// Read when the file changes: wait until the server's configuration has it.
		config, err := f.fixture.Server.Host.Hold(p2test.Background, modelscontract.ModelConfig, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p2test.Until(config.Member("snapshot"), func(value delta.JsonValue) bool {
			return object(object(value).Value("settings")).Value("viewRoomsMemory") == 1.0
		}, 0); err != nil {
			t.Fatal(err)
		}
		config.Release()
		sessions, err := p2test.HoldIn(f.owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", f.fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		id := object(created).Value("id").(string)
		chat, err := p2test.HoldIn(f.owner, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", 1.0), "participant")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "view "+viewsJSON(t, map[string]any{"action": "open", "kind": "custom", "title": "Room", "id": "room", "html": roomPage}), "requestId", "budget-open-"+id), p2test.Background); err != nil {
			t.Fatal(err)
		}
		opening := ""
		for n := 0; n < 100 && opening == ""; n++ {
			links := f.links[id]
			if links == nil {
				if links, err = p2test.HoldIn(f.owner, viewscontract.ViewLinks, delta.JsonObjectOf("session", id), "viewer"); err != nil {
					t.Fatal(err)
				}
				f.links[id] = links
			}
			answer, err := links.Call("token", delta.JsonObjectOf("view", "room"), p2test.Background)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			opening, _ = object(answer).Value("opening").(string)
		}
		if opening == "" {
			t.Fatal("the view never opened")
		}
		return f, roomSession{id: id, chat: chat}, opening, nil
	}
	writer := func(f *roomsFixture, id, opening, linkID string) *host.Reference {
		return f.hold(f.fixture.As(p2runtime.ViewPrincipal{Session: id, View: "room", Opening: opening, Mode: p2runtime.ViewWrite, ForName: linkID, ForRole: "owner", Link: linkID, Editor: linkID, Viewer: []p2runtime.Revocation{}}), id, opening, "peer")
	}

	t.Run("refuses a snapshot the room cannot take before anything is written", func(t *testing.T) {
		f, s, opening, _ := start(t)
		leader := writer(f, s.id, opening, "leader")
		call(t, leader, "join", delta.JsonObjectOf("kind", "log"))
		// About 920 KB kept: a small message, then two large ones.
		call(t, leader, "send", send(map[string]any{"data": "1"}, map[string]any{"data": strings.Repeat("a", 230_000)}, map[string]any{"data": strings.Repeat("b", 230_000)}))
		room := untilMember(t, leader, "room", func(value *delta.JsonObject) bool { return intOfValue(value.Value("seq")) == 3 })
		base := object(room.Value("base"))
		// A snapshot after the small message frees almost nothing, and its 240 KB base would pass the budget.
		err := callErr(leader, "snapshot", delta.JsonObjectOf("seq", 1.0, "epoch", base.Value("epoch"), "term", room.Value("term"), "value", delta.JsonObjectOf("text", strings.Repeat("z", 120_000))))
		if err == nil || !strings.Contains(err.Error(), "keep too much memory") {
			t.Fatalf("the snapshot: %v", err)
		}
		// Nothing persisted, the room as it was.
		state, err := p2test.HoldIn(f.owner, viewscontract.ViewState, delta.JsonObjectOf("session", s.id, "view", "room"), "editor")
		if err != nil {
			t.Fatal(err)
		}
		if size := len(viewsJSON(t, member(state, "value"))); size >= 1000 {
			t.Errorf("the view's state is %d characters", size)
		}
		if seq := intOfValue(object(member(leader, "room").Value("base")).Value("seq")); seq != 0 {
			t.Errorf("the base moved to %d", seq)
		}
		if first := object(list(call(t, leader, "history", delta.JsonObjectOf("after", 0.0, "limit", 10.0)).Value("messages"))[0]); intOfValue(first.Value("seq")) != 1 {
			t.Errorf("the journal starts at %v", first.Value("seq"))
		}
	})

	t.Run("counts concurrent trims of the same messages once", func(t *testing.T) {
		f, s, opening, _ := start(t)
		left, right := writer(f, s.id, opening, "left"), writer(f, s.id, opening, "right")
		call(t, left, "join", delta.JsonObjectOf("kind", "live"))
		call(t, right, "join", delta.JsonObjectOf("kind", "live"))
		// About 2 KiB a message as charged: 400 of them are most of the 1 MiB budget.
		batch := make([]map[string]any, 100)
		for n := range batch {
			batch[n] = map[string]any{"data": strings.Repeat("z", 1000)}
		}
		fill := func() error {
			for range 4 {
				if err := callErr(left, "send", send(batch...)); err != nil {
					return err
				}
			}
			return nil
		}
		// The budget applies once settings.json was read.
		empty := make([]map[string]any, 1000)
		for n := range empty {
			empty[n] = map[string]any{"data": ""}
		}
		applied := false
		for n := 0; n < 100 && !applied; n++ {
			applied = callErr(left, "send", send(empty...)) != nil
			time.Sleep(50 * time.Millisecond)
		}
		if !applied {
			t.Fatal("the budget does not apply")
		}
		// Everything acknowledged by both peers at once, several times over: trims race for the same blocks.
		everything := func() {
			seq := member(left, "room").Value("seq")
			var pending []*host.PendingCall
			for _, each := range []*host.Reference{left, right, left, right} {
				started, err := each.Start("ack", delta.JsonObjectOf("seq", seq), p2test.Background)
				if err != nil {
					t.Fatal(err)
				}
				pending = append(pending, started)
			}
			for _, each := range pending {
				if _, err := each.Wait(p2test.Background); err != nil {
					t.Fatal(err)
				}
			}
		}
		everything()
		if err := fill(); err != nil {
			t.Fatal(err)
		}
		everything()
		// Counted once, the room is empty again: 400 messages fit, 800 do not.
		if err := fill(); err != nil {
			t.Fatal(err)
		}
		if err := fill(); err == nil || !strings.Contains(err.Error(), "keep too much memory") {
			t.Fatalf("the room took 800 messages: %v", err)
		}
	})
}
