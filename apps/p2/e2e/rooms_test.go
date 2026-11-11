// Ports test/p2/rooms.test.ts (Pi main 682c7b93c), with the fake provider: rooms of custom views (plugins/views/rooms.go, apps/p2/docs/
// rooms.md): tokens and the view link, the view principal's reach and revocation, presence and the leader, keyed and unkeyed messages,
// log and text rooms, and the agent's calls to methods a frame offers. Each case opens its own session and view; the inputs are Pi's.
// (Pi's ui-rooms.test.ts, two Chromium browsers sharing a text room, is not ported: it needs the browser the web gate runs.)

package e2e_test

import (
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	viewscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/gorilla/websocket"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/views"
)

const roomPage = "<!doctype html><html><head><title>Room</title></head><body>room</body></html>"

// roomsFixture is a server with a browser (the owner's) and the sessions of the cases.
type roomsFixture struct {
	t       *testing.T
	fixture *p2test.Fixture
	owner   host.Host
	sent    int
	links   map[string]*host.Reference
}

func newRoomsFixture(t *testing.T, options hosts.ServerOptions) *roomsFixture {
	t.Helper()
	p2test.NoLeakedGoroutines(t)
	f := &roomsFixture{t: t, fixture: p2test.StartFixture(t, options), links: map[string]*host.Reference{}}
	f.owner = f.fixture.Browser()
	return f
}

type roomSession struct {
	id   string
	chat *host.Reference
}

// session is a new session with the custom view `room` open, and its chat.
func (f *roomsFixture) session() roomSession {
	f.t.Helper()
	sessions, err := p2test.HoldIn(f.owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		f.t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", f.fixture.Workspace), p2test.Background)
	if err != nil {
		f.t.Fatal(err)
	}
	id := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(f.owner, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", 1.0), "participant")
	if err != nil {
		f.t.Fatal(err)
	}
	f.prompt(chat, "view", "view "+viewsJSON(f.t, map[string]any{"action": "open", "kind": "custom", "title": "Room", "id": "room", "html": roomPage}))
	return roomSession{id: id, chat: chat}
}

// prompt has the agent run line (a fake-provider prompt calling tool); that call's result text.
func (f *roomsFixture) prompt(chat *host.Reference, tool, line string) string {
	f.t.Helper()
	current, _ := chat.Member("view").Value()
	before := len(toolResults(current, tool))
	f.sent++
	if _, err := chat.Call("send", delta.JsonObjectOf("text", line, "requestId", "rooms-"+strconv.Itoa(f.sent)), p2test.Background); err != nil {
		f.t.Fatal(err)
	}
	view, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool { return len(toolResults(value, tool)) == before+1 && idle(value) }, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	results := toolResults(view, tool)
	return toolText(results[len(results)-1])
}

// token is a token for view of session, as the owner's page asks for one (its tokens live while the page holds it).
func (f *roomsFixture) token(session, view string, editor ...string) (token, opening, editorID string) {
	f.t.Helper()
	links := f.links[session]
	if links == nil {
		var err error
		if links, err = p2test.HoldIn(f.owner, viewscontract.ViewLinks, delta.JsonObjectOf("session", session), "viewer"); err != nil {
			f.t.Fatal(err)
		}
		f.links[session] = links
	}
	args := delta.JsonObjectOf("view", view)
	if len(editor) > 0 {
		args.Set("editor", editor[0])
	}
	answer, err := links.Call("token", args, p2test.Background)
	if err != nil {
		f.t.Fatal(err)
	}
	return object(answer).Value("token").(string), object(answer).Value("opening").(string), object(answer).Value("editor").(string)
}

// viewSocket is a socket to /chord/view as a sandboxed frame opens it (Origin null), with headers besides.
func (f *roomsFixture) viewSocket(value string, headers map[string]string) (*websocket.Conn, error) {
	all := map[string]string{"Origin": "null"}
	for name, header := range headers {
		all[name] = header
	}
	return p2test.DialPath(f.fixture.Server.Port, "/chord/view?token="+url.QueryEscape(value), all)
}

// refused is whether the upgrade of a dial was refused.
func socketRefused(conn *websocket.Conn, err error) bool {
	if err == nil {
		_ = conn.Close()
	}
	return err != nil
}

func peerParams(session, opening string) *delta.JsonObject {
	return delta.JsonObjectOf("session", session, "view", "room", "opening", opening)
}

func (f *roomsFixture) hold(h host.Host, session, opening, variant string) *host.Reference {
	f.t.Helper()
	ref, err := p2test.HoldIn(h, viewscontract.ViewRoom, peerParams(session, opening), variant)
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}

// frame is a frame of session's view over the real token path: its host and its room.
func (f *roomsFixture) frame(session string) (host.Host, *host.Reference, string) {
	f.t.Helper()
	value, opening, _ := f.token(session, "room")
	conn, err := f.viewSocket(value, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	client, err := p2test.ClientHost(conn)
	if err != nil {
		f.t.Fatal(err)
	}
	return client, f.hold(client, session, opening, "peer"), opening
}

// principalOf is a view principal made here: link is its peer ID.
func principalOf(session, opening, linkID string, mode p2runtime.ViewMode, viewer p2runtime.Principal) p2runtime.ViewPrincipal {
	return p2runtime.ViewPrincipal{
		Session: session, View: "room", Opening: opening, Mode: mode, ForName: linkID, ForRole: "owner", Link: linkID, Editor: "editor-" + linkID,
		Viewer: p2runtime.RevokedBy(viewer),
	}
}

var localOwner p2runtime.Principal = p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}

// crafted is a frame accepted with a principal made here (memory link): link is its peer ID.
func (f *roomsFixture) crafted(session, opening, linkID string, mode ...p2runtime.ViewMode) *host.Reference {
	f.t.Helper()
	return f.craftedFor(session, opening, linkID, localOwner, mode...)
}

func (f *roomsFixture) craftedFor(session, opening, linkID string, viewer p2runtime.Principal, mode ...p2runtime.ViewMode) *host.Reference {
	f.t.Helper()
	kind := p2runtime.ViewWrite
	if len(mode) > 0 {
		kind = mode[0]
	}
	return f.hold(f.fixture.As(principalOf(session, opening, linkID, kind, viewer)), session, opening, "peer")
}

// stalledLink is a frame's end of a memory link whose server-to-frame side can stall (deliveries held back) and be released.
type stalledLink struct {
	link.Link
	mu       sync.Mutex
	held     [][]byte
	stalling bool
	counting bool
	bytes    int
	deliver  func([]byte)
}

func (s *stalledLink) OnMessage(listener func(message []byte)) {
	s.mu.Lock()
	s.deliver = func(message []byte) {
		s.mu.Lock()
		if s.counting {
			s.bytes += len(message)
		}
		s.mu.Unlock()
		listener(message)
	}
	s.mu.Unlock()
	s.Link.OnMessage(func(message []byte) {
		s.mu.Lock()
		if s.stalling {
			s.held = append(s.held, message)
			s.mu.Unlock()
			return
		}
		deliver := s.deliver
		s.mu.Unlock()
		deliver(message)
	})
}

func (s *stalledLink) stall() {
	s.mu.Lock()
	s.stalling = true
	s.mu.Unlock()
}

// release delivers what was held and everything after; the function returns the bytes delivered from then on, once quiet.
func (s *stalledLink) release() func() int {
	s.mu.Lock()
	queued := s.held
	s.held, s.stalling, s.counting = nil, false, true
	deliver := s.deliver
	s.mu.Unlock()
	for _, message := range queued {
		deliver(message)
	}
	return func() int {
		time.Sleep(200 * time.Millisecond)
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.bytes
	}
}

// stalledFrame is a frame over a memory link that can stall.
func (f *roomsFixture) stalledFrame(session, opening, linkID string) (*host.Reference, *stalledLink) {
	f.t.Helper()
	down, up := link.MemoryLinkPair()
	if err := f.fixture.Server.Host.Accept(down, host.LinkOptions{Principal: p2runtime.ToChord(principalOf(session, opening, linkID, p2runtime.ViewWrite, localOwner))}, p2test.Background); err != nil {
		f.t.Fatal(err)
	}
	stalling := &stalledLink{Link: up}
	client, err := p2runtime.NewHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := client.Connect(stalling, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, p2test.Background); err != nil {
		f.t.Fatal(err)
	}
	if _, err := client.Load(nil, p2test.Background); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = client.Close(p2test.Background) })
	return f.hold(client, session, opening, "peer"), stalling
}

func call(t *testing.T, ref *host.Reference, name string, args *delta.JsonObject) *delta.JsonObject {
	t.Helper()
	result, err := ref.Call(name, args, p2test.Background)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return object(result)
}

func callErr(ref *host.Reference, name string, args *delta.JsonObject) error {
	_, err := ref.Call(name, args, p2test.Background)
	return err
}

func send(messages ...map[string]any) *delta.JsonObject {
	list := make([]any, len(messages))
	for i, message := range messages {
		entry := delta.JsonObjectOf("data", message["data"])
		if key, ok := message["key"]; ok {
			entry.Set("key", key)
		}
		list[i] = entry
	}
	return delta.JsonObjectOf("messages", list)
}

func untilMember(t *testing.T, ref *host.Reference, member string, predicate func(*delta.JsonObject) bool) *delta.JsonObject {
	t.Helper()
	value, err := p2test.Until(ref.Member(member), func(value delta.JsonValue) bool { return value != nil && predicate(object(value)) }, 0)
	if err != nil {
		t.Fatal(err)
	}
	return object(value)
}

func member(ref *host.Reference, name string) *delta.JsonObject {
	value, _ := ref.Member(name).Value()
	return object(value)
}

func requireFailure(t *testing.T, ref *host.Reference, code data.ChordErrorCode) {
	t.Helper()
	failure, err := p2test.FailureOf(ref)
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, failure, code)
}
