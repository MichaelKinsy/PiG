package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	viewscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestViewRooms(t *testing.T) {
	f := newRoomsFixture(t, hosts.ServerOptions{})
	ownerHeaders := func(extra map[string]string) map[string]string { return f.fixture.Owner().OwnerHeaders(extra) }
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

	t.Run("admits a frame's link by its token alone, once", func(t *testing.T) {
		s := f.session()
		value, opening, _ := f.token(s.id, "room")
		// The wrong Host with a valid token is refused (DNS rebinding), and the token is not spent by it.
		if !socketRefused(f.viewSocket(value, map[string]string{"Host": "evil.example"})) {
			t.Fatal("a bad Host was admitted")
		}
		conn, err := f.viewSocket(value, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := p2test.ClientHost(conn)
		if err != nil {
			t.Fatal(err)
		}
		room := f.hold(client, s.id, opening, "peer")
		if me, _ := join(room, "live").Value("me").(string); me == "" {
			t.Fatal("no peer ID")
		}
		// Used: refused now. No token, cookies only: refused. Origin null on the ordinary /chord: refused.
		if !socketRefused(f.viewSocket(value, nil)) {
			t.Error("a used token was admitted")
		}
		if !socketRefused(p2test.DialPath(f.fixture.Server.Port, "/chord/view", ownerHeaders(map[string]string{"Origin": "null"}))) {
			t.Error("cookies alone were admitted")
		}
		if !socketRefused(p2test.DialPath(f.fixture.Server.Port, "/chord", ownerHeaders(map[string]string{"Origin": "null"}))) {
			t.Error("Origin null on /chord was admitted")
		}
		// A sandboxed frame's `Origin: null` reaches the view kit only.
		page, err := f.fixture.HTTP("/view-frame?session="+s.id+"&view=room&version=1", p2test.HTTPOptions{Headers: ownerHeaders(nil)})
		if err != nil {
			t.Fatal(err)
		}
		found := regexp.MustCompile(`src="(/view-kit/[0-9a-f]{64}\.js)"`).FindStringSubmatch(page.Body)
		if found == nil {
			t.Fatalf("the frame's page names no kit: %s", page.Body)
		}
		for path, want := range map[string]int{found[1]: 200, "/health": 403, "/view-frame?session=" + s.id + "&view=room&version=1": 403} {
			headers := ownerHeaders(map[string]string{"Origin": "null"})
			if path == found[1] {
				headers = map[string]string{"Origin": "null", "Cookie": ""}
			}
			result, err := f.fixture.HTTP(path, p2test.HTTPOptions{Headers: headers})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != want {
				t.Errorf("Origin null %s: %d, want %d", path, result.Status, want)
			}
		}
		// Two redemptions at once: one link.
		twice, _, _ := f.token(s.id, "room")
		var wg sync.WaitGroup
		outcomes := make([]bool, 2)
		for i := range outcomes {
			wg.Add(1)
			go func() { defer wg.Done(); outcomes[i] = socketRefused(f.viewSocket(twice, nil)) }()
		}
		wg.Wait()
		if outcomes[0] == outcomes[1] {
			t.Errorf("two redemptions of one token: refused %v", outcomes)
		}
	})

	t.Run("reaches nothing but its own view's room", func(t *testing.T) {
		s := f.session()
		client, _, opening := f.frame(s.id)
		for name, hold := range map[string]func() (*host.Reference, error){
			"Sessions": func() (*host.Reference, error) {
				return client.Hold(p2test.Background, sessionscontract.Sessions, delta.NewJsonObject(0), host.GetOptions{})
			},
			"Views": func() (*host.Reference, error) {
				return client.Hold(p2test.Background, viewscontract.Views, delta.JsonObjectOf("session", s.id), host.GetOptions{})
			},
			"ViewState": func() (*host.Reference, error) {
				return client.Hold(p2test.Background, viewscontract.ViewState, delta.JsonObjectOf("session", s.id, "view", "room"), host.GetOptions{})
			},
			"ViewLinks": func() (*host.Reference, error) {
				return client.Hold(p2test.Background, viewscontract.ViewLinks, delta.JsonObjectOf("session", s.id), host.GetOptions{})
			},
			"another view's room": func() (*host.Reference, error) {
				return client.Hold(p2test.Background, viewscontract.ViewRoom, delta.JsonObjectOf("session", s.id, "view", "other", "opening", opening), host.GetOptions{})
			},
		} {
			ref, err := hold()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			requireFailure(t, ref, data.CodeForbidden)
		}
	})

	t.Run("ends a frame's room when its view closes, and refuses the old opening's tokens after it reopens", func(t *testing.T) {
		s := f.session()
		_, room, _ := f.frame(s.id)
		stale, staleOpening, _ := f.token(s.id, "room")
		f.prompt(s.chat, "view", "view "+viewsJSON(t, map[string]any{"action": "close", "id": "room"}))
		requireFailure(t, room, data.CodeForbidden)
		f.prompt(s.chat, "view", "view "+viewsJSON(t, map[string]any{"action": "open", "kind": "custom", "title": "Room", "id": "room", "html": roomPage}))
		if !socketRefused(f.viewSocket(stale, nil)) {
			t.Error("a token of the old opening was admitted")
		}
		if _, opening, _ := f.token(s.id, "room"); opening == staleOpening {
			t.Error("the reopened view has its old opening")
		}
	})

	t.Run("ends a frame's room when its viewer's grant is revoked", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		guest := p2runtime.GuestPrincipal{ID: "g1", Name: "Guest", Home: s.id, Sessions: map[string]p2runtime.GuestMode{s.id: p2runtime.GuestView}, Revision: 1}
		room := f.craftedFor(s.id, opening, "guest-frame", guest, p2runtime.ViewRead)
		if _, err := f.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			server, err := ctx.Get(p2runtime.ServerLocal, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			return server.Call("revokeGuests", delta.JsonObjectOf("id", "g1"), ctx)
		}, p2test.Background, host.RootOptions{}); err != nil {
			t.Fatal(err)
		}
		requireFailure(t, room, data.CodeForbidden)
	})

	t.Run("ends every frame of a view's opening when the server revokes it", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		room := f.crafted(s.id, opening, "doomed")
		_, real, _ := f.frame(s.id)
		if _, err := f.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			server, err := ctx.Get(p2runtime.ServerLocal, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			return server.Call("revokeFrames", delta.JsonObjectOf("view", p2runtime.ViewKey(s.id, "room", opening)), ctx)
		}, p2test.Background, host.RootOptions{}); err != nil {
			t.Fatal(err)
		}
		// Read by polling: a socket's reference ends as its link closes.
		for _, ref := range []*host.Reference{room, real} {
			var state *host.RefState
			for n := 0; n < 100; n++ {
				if state = ref.State().Value(); state.Kind == host.RefStateEnded {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			code := "no error"
			if state.Kind == host.RefStateEnded && state.Error != nil {
				if chordErr, ok := data.IsChordError(state.Error); ok {
					code = string(chordErr.Code)
				}
			} else {
				code = string(state.Kind)
			}
			if code != string(data.CodeForbidden) {
				t.Errorf("a revoked frame's room ends %s, want forbidden", code)
			}
		}
	})

	t.Run("names the oldest frame that may write the leader, and hands over when it leaves", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		reader := f.crafted(s.id, opening, "reader", p2runtime.ViewRead)
		first := f.crafted(s.id, opening, "first")
		second := f.crafted(s.id, opening, "second")
		presence := untilMember(t, second, "presence", func(value *delta.JsonObject) bool { return len(list(value.Value("peers"))) == 3 })
		if presence.Value("leader") != "first" {
			t.Fatalf("leader = %v", presence.Value("leader"))
		}
		var got [][2]string
		for _, each := range list(presence.Value("peers")) {
			got = append(got, [2]string{object(each).Value("id").(string), object(each).Value("mode").(string)})
		}
		if want := [][2]string{{"reader", "read"}, {"first", "write"}, {"second", "write"}}; !equalPairs(got, want) {
			t.Fatalf("peers = %v, want %v", got, want)
		}
		p2test.ExpectChord(t, callErr(reader, "send", send(map[string]any{"data": "1"})), data.CodeForbidden)
		first.Release()
		untilMember(t, second, "presence", func(value *delta.JsonObject) bool { return value.Value("leader") == "second" })
	})

	t.Run("folds a key's updates for a peer whose link stalls, and gives joiners each peer's latest", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		sender := f.crafted(s.id, opening, "sender")
		join(sender, "live")
		roomCall(sender, "send", send(map[string]any{"key": "cursor", "data": `"start"`}))
		// The slow peer's link holds back what the server sends until released: its acknowledgements stop too, so the server's window
		// fills and the rest waits in its pending queue.
		room, slow := f.stalledFrame(s.id, opening, "slow")
		cursorData := func(ref *host.Reference) string {
			keyed := member(ref, "keyed")
			text, _ := object(object(object(keyed.Value("peers")).Value("sender")).Value("cursor")).Value("data").(string)
			if text == "" {
				return ""
			}
			return text[1:]
		}
		untilMember(t, room, "keyed", func(*delta.JsonObject) bool { return cursorData(room) == `"start"` })
		slow.stall()
		big := func(n int) string {
			encoded, _ := json.Marshal(strconv.Itoa(n) + ":" + strings.Repeat("x", 2000))
			return string(encoded)
		}
		for n := 1; n <= 50; n++ {
			roomCall(sender, "send", send(map[string]any{"key": "cursor", "data": big(n)}))
		}
		bytes := slow.release()
		untilMember(t, room, "keyed", func(*delta.JsonObject) bool { return cursorData(room) == big(50) })
		// What reached the slow peer: the messages in its window and one folded update, not fifty values of 2 KB.
		if received := bytes(); received >= 12*1024 {
			t.Errorf("bytes: %d", received)
		}
		joiner := f.crafted(s.id, opening, "joiner")
		if got := cursorData(joiner); got != big(50) {
			t.Errorf("a joiner's cursor = %.40s", got)
		}
	})

	t.Run("refuses a keyed message that would make the keyed snapshot too large for a joiner", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		sender := f.crafted(s.id, opening, "sender")
		join(sender, "live")
		encoded, _ := json.Marshal(strings.Repeat("y", 250*1024))
		for n := 0; n < 4; n++ {
			roomCall(sender, "send", send(map[string]any{"key": "k" + strconv.Itoa(n), "data": string(encoded)}))
		}
		if err := callErr(sender, "send", send(map[string]any{"key": "k4", "data": string(encoded)})); err == nil || !strings.Contains(err.Error(), "part limit") {
			t.Fatalf("the fifth keyed message: %v", err)
		}
		joiner := f.crafted(s.id, opening, "joiner")
		if keys := len(object(object(member(joiner, "keyed").Value("peers")).Value("sender")).Keys()); keys != 4 {
			t.Errorf("a joiner sees %d keys, want 4", keys)
		}
	})

	t.Run("counts the keyed snapshot as encoded: escapes and non-ASCII text", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		for _, each := range [][2]string{{"quotes", `"`}, {"backslashes", `\`}, {"non-ASCII", "€"}} {
			name, unit := each[0], each[1]
			sender := f.crafted(s.id, opening, "sender-"+name)
			join(sender, "live")
			// 100,000 characters each: escaped or UTF-8, each key encodes to 200-300 KB.
			accepted := 0
			for n := 0; n < 8; n++ {
				err := callErr(sender, "send", send(map[string]any{"key": "k" + strconv.Itoa(n), "data": strings.Repeat(unit, 100_000)}))
				if err == nil {
					accepted++
				} else if !strings.Contains(err.Error(), "part limit") {
					t.Errorf("%s: %v", name, err)
				}
			}
			if accepted >= 8 {
				t.Errorf("%s: none refused", name)
			}
			// What a joiner gets fits the link's part limit.
			joiner := f.crafted(s.id, opening, "joiner-"+name)
			keyed := member(joiner, "keyed")
			if keys := len(object(object(keyed.Value("peers")).Value("sender-" + name)).Keys()); keys != accepted {
				t.Errorf("%s: a joiner sees %d keys, accepted %d", name, keys, accepted)
			}
			encoded, _ := json.Marshal(keyed)
			if len(encoded) > 1024*1024 {
				t.Errorf("%s: the keyed member is %d bytes", name, len(encoded))
			}
			sender.Release()
			joiner.Release()
		}
	})

	t.Run("never lets a frame that may only watch change or reset the room", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		writer := f.crafted(s.id, opening, "writer")
		join(writer, "log")
		roomCall(writer, "send", send(map[string]any{"data": `"move"`}))
		reader := f.crafted(s.id, opening, "reader", p2runtime.ViewRead)
		join(reader, "log")
		writer.Release()
		untilMember(t, reader, "presence", func(value *delta.JsonObject) bool { return len(list(value.Value("peers"))) == 1 })
		p2test.ExpectChord(t, callErr(reader, "join", delta.JsonObjectOf("kind", "live")), data.CodeForbidden)
		if kind := member(reader, "room").Value("kind"); kind != "log" {
			t.Errorf("the room is a %v room", kind)
		}
		messages := list(member(reader, "log").Value("messages"))
		if len(messages) != 1 || object(messages[0]).Value("data") != `"move"` {
			t.Errorf("log = %v", messages)
		}
	})

	t.Run("keeps appending at a flat cost however many messages the room keeps", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		sender := f.crafted(s.id, opening, "sender")
		// A peer that never acknowledges: the room keeps everything.
		quiet := f.crafted(s.id, opening, "quiet")
		join(sender, "live")
		join(quiet, "live")
		batch := make([]map[string]any, 500)
		for n := range batch {
			batch[n] = map[string]any{"data": strconv.Itoa(n)}
		}
		timed := func() time.Duration {
			start := time.Now()
			for n := 0; n < 4; n++ {
				roomCall(sender, "send", send(batch...))
			}
			return time.Since(start)
		}
		early := timed()
		for n := 0; n < 40; n++ {
			roomCall(sender, "send", send(batch...))
		}
		late := timed()
		// 22,000 kept messages: an append drafts one block of the journal, never all of it.
		if late >= early*3+100*time.Millisecond {
			t.Errorf("early %v, late %v", early, late)
		}
		page := roomCall(quiet, "history", delta.JsonObjectOf("after", 0.0, "limit", 1000.0))
		if len(list(page.Value("messages"))) != 1000 || page.Value("more") != true {
			t.Errorf("history = %d messages, more %v", len(list(page.Value("messages"))), page.Value("more"))
		}
	})

	t.Run("charges every kept message to the memory budget, empty ones too", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		sender := f.crafted(s.id, opening, "sender")
		f.crafted(s.id, opening, "quiet")
		join(sender, "live")
		settings := filepath.Join(f.fixture.DataDir, "settings.json")
		if err := os.WriteFile(settings, []byte(`{"viewRoomsMemory":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		defer os.WriteFile(settings, []byte("{}"), 0o600)
		empty := make([]map[string]any, 1000)
		for n := range empty {
			empty[n] = map[string]any{"data": ""}
		}
		var refusal error
		// settings.json is read when it changes: sending goes on until the budget applies and the room is full.
		for n := 0; n < 100 && refusal == nil; n++ {
			refusal = callErr(sender, "send", send(empty...))
			time.Sleep(50 * time.Millisecond)
		}
		if refusal == nil || !strings.Contains(refusal.Error(), "keep too much memory") {
			t.Fatalf("refusal = %v", refusal)
		}
	})

	t.Run("delivers unkeyed messages in order to every peer and drops them once all acknowledged", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		left := f.crafted(s.id, opening, "left")
		right := f.crafted(s.id, opening, "right")
		join(left, "live")
		join(right, "live")
		roomCall(left, "send", send(map[string]any{"data": `"a"`}, map[string]any{"key": "pos", "data": "1"}, map[string]any{"data": `"b"`}))
		roomCall(right, "send", send(map[string]any{"data": `"c"`}))
		log := untilMember(t, left, "log", func(value *delta.JsonObject) bool { return len(list(value.Value("messages"))) == 3 })
		var got [][2]string
		for _, each := range list(log.Value("messages")) {
			got = append(got, [2]string{object(each).Value("from").(string), object(each).Value("data").(string)})
		}
		if want := [][2]string{{"left", `"a"`}, {"left", `"b"`}, {"right", `"c"`}}; !equalPairs(got, want) {
			t.Fatalf("log = %v, want %v", got, want)
		}
		// Keyed and unkeyed share one numbering: the keyed message sits between a and b.
		messages := list(log.Value("messages"))
		pos := intOfValue(object(object(object(member(left, "keyed").Value("peers")).Value("left")).Value("pos")).Value("seq"))
		if first, second := intOfValue(object(messages[0]).Value("seq")), intOfValue(object(messages[1]).Value("seq")); !(first < pos && pos < second) {
			t.Errorf("seq %d, keyed %d, seq %d", first, pos, second)
		}
		last := object(messages[2]).Value("seq")
		roomCall(left, "ack", delta.JsonObjectOf("seq", last))
		if n := len(list(member(right, "log").Value("messages"))); n != 3 {
			t.Errorf("kept while one peer has not acknowledged: %d messages", n)
		}
		roomCall(right, "ack", delta.JsonObjectOf("seq", last))
		untilMember(t, right, "log", func(value *delta.JsonObject) bool { return len(list(value.Value("messages"))) == 0 })
	})

	t.Run("keeps a log room's messages since the leader's snapshot, and resets it to the agent's state", func(t *testing.T) {
		s := f.session()
		_, opening, _ := f.token(s.id, "room")
		leader := f.crafted(s.id, opening, "leader")
		join(leader, "log")
		roomCall(leader, "send", send(map[string]any{"data": "1"}, map[string]any{"data": "2"}))
		room := untilMember(t, leader, "room", func(value *delta.JsonObject) bool { return intOfValue(value.Value("seq")) == 2 })
		base := object(room.Value("base"))
		if base == nil {
			t.Fatal("no base")
		}
		term := intOfValue(room.Value("term"))
		p2test.ExpectChord(t, callErr(leader, "snapshot", delta.JsonObjectOf("seq", 2.0, "epoch", base.Value("epoch"), "term", float64(term-1), "value", delta.JsonObjectOf("total", 3.0))), data.CodeInvalid)
		roomCall(leader, "snapshot", delta.JsonObjectOf("seq", 2.0, "epoch", base.Value("epoch"), "term", float64(term), "value", delta.JsonObjectOf("total", 3.0)))
		untilMember(t, leader, "log", func(value *delta.JsonObject) bool { return len(list(value.Value("messages"))) == 0 })
		state, err := p2test.HoldIn(f.owner, viewscontract.ViewState, delta.JsonObjectOf("session", s.id, "view", "room"), "editor")
		if err != nil {
			t.Fatal(err)
		}
		defer state.Release()
		if _, err := p2test.Until(state.Member("value"), func(value delta.JsonValue) bool { return viewsJSON(t, value) == `{"total":3}` }, 0); err != nil {
			t.Fatal(err)
		}
		f.prompt(s.chat, "view", "view "+viewsJSON(t, map[string]any{"action": "update", "id": "room", "state": map[string]any{"total": 10}}))
		reset := untilMember(t, leader, "room", func(value *delta.JsonObject) bool {
			return object(value.Value("base")) != nil && viewsJSON(t, object(value.Value("base")).Value("value")) == `{"total":10}`
		})
		if got, want := intOfValue(object(reset.Value("base")).Value("epoch")), intOfValue(base.Value("epoch"))+1; got != want {
			t.Errorf("epoch = %d, want %d", got, want)
		}
	})
}

func intOfValue(value any) int {
	number, _ := value.(float64)
	return int(number)
}

func equalPairs(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
