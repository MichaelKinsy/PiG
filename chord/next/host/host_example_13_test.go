package host

// pi: packages/chord/test/next/examples/13-relay.ts

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// --- 13-relay.ts ---------------------------------------------------------------------------------------------------

// exFramedPort is an IpcPort over a byte stream pair (a child's stdin and stdout): Pi's Node IPC channel with
// `serialization: "advanced"`. A frame is a 4-byte length and a leading 0x00 for a link message; anything else is the
// app's own message. Node emits a channel's messages from its event loop, after the synchronous section that made the
// link (`ipcLink(process)`, `ipcLink(child)`) and so registered its listeners: no message arrives before them. The port
// reads nothing until start, which the code that made the link calls once it has.
type exFramedPort struct {
	reader io.Reader
	writer io.WriteCloser
	mu     sync.Mutex

	connected   bool
	messages    []func(any)
	disconnects []func()
}

func newExFramedPort(reader io.Reader, writer io.WriteCloser) *exFramedPort {
	return &exFramedPort{reader: reader, writer: writer, connected: true}
}

// start begins the delivery of the stream's frames to the registered listeners: the event loop's first turn after the
// section that registered them.
func (p *exFramedPort) start() { go p.read() }

func (p *exFramedPort) read() {
	for {
		var length [4]byte
		if _, err := io.ReadFull(p.reader, length[:]); err != nil {
			p.ended()
			return
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(p.reader, frame); err != nil {
			p.ended()
			return
		}
		p.mu.Lock()
		listeners := slices.Clone(p.messages)
		p.mu.Unlock()
		for _, listener := range listeners {
			if frame[0] == 0 {
				listener(frame[1:])
			} else {
				listener(string(frame[1:]))
			}
		}
	}
}

func (p *exFramedPort) ended() {
	p.mu.Lock()
	was := p.connected
	p.connected = false
	listeners := slices.Clone(p.disconnects)
	p.mu.Unlock()
	if was {
		for _, listener := range listeners {
			listener()
		}
	}
}

func (p *exFramedPort) Send(message []byte, callback func(error)) (bool, error) {
	frame := make([]byte, 4, 5+len(message))
	binary.BigEndian.PutUint32(frame, uint32(1+len(message)))
	frame = append(append(frame, 0), message...)
	p.mu.Lock()
	_, err := p.writer.Write(frame)
	p.mu.Unlock()
	callback(err)
	return err == nil, nil
}

func (p *exFramedPort) OnMessage(listener func(message any)) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, listener)
	return func() {}
}

func (p *exFramedPort) OnDisconnect(listener func()) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disconnects = append(p.disconnects, listener)
	return func() {}
}

func (p *exFramedPort) Disconnect() {
	p.mu.Lock()
	p.connected = false
	p.mu.Unlock()
	_ = p.writer.Close()
}

func (p *exFramedPort) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

// exSocket is a gorilla WebSocket as link.WebSocketLike: binary frames, one message per frame.
type exSocket struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	listeners map[link.WebSocketEvent][]func(any)
	writeMu   sync.Mutex
	closed    bool
}

func newExSocket(conn *websocket.Conn) *exSocket {
	socket := &exSocket{conn: conn, listeners: map[link.WebSocketEvent][]func(any){}}
	return socket
}

func (s *exSocket) start() { go s.read() }

func (s *exSocket) read() {
	for {
		kind, payload, err := s.conn.ReadMessage()
		if err != nil {
			s.emit(link.WebSocketClose, nil)
			return
		}
		if kind == websocket.BinaryMessage {
			s.emit(link.WebSocketMessage, payload)
		} else {
			s.emit(link.WebSocketMessage, string(payload))
		}
	}
}

func (s *exSocket) emit(event link.WebSocketEvent, value any) {
	s.mu.Lock()
	listeners := slices.Clone(s.listeners[event])
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(value)
	}
}

func (s *exSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 3
	}
	return 1
}

func (s *exSocket) SetBinaryType(string) {}

func (s *exSocket) Send(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (s *exSocket) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.conn.Close()
}

func (s *exSocket) AddEventListener(event link.WebSocketEvent, listener func(data any)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners[event] = append(s.listeners[event], listener)
	return func() {}
}

// exRelayWorkerEnv names the environment variable that makes the test binary the worker of the relay example: Pi's
// `process.argv[2] === "worker"` (the same file is the worker).
const exRelayWorkerEnv = "CHORD_EXAMPLE_13_WORKER"

func exRelayContracts() (sessionInfo, notes *contracts.Contract) {
	initSchema := exObj(exProp("id", exStr()), exProp("title", exStr()), exProp("pid", exInt()))
	sessionInfo = exLocal("example.session-info", contracts.Params{}, contracts.Mem("init", initSchema))
	notes = exRemote("example.notes", contracts.Params{exProp("session", exStr())}, exSessionAddress,
		contracts.Var("owner", contracts.VariantDeclaration{
			State:   []contracts.Property{exProp("info", initSchema), exProp("notes", exArr(exStr()))},
			Methods: []contracts.MethodEntry{contracts.Meth("add", contracts.NewMethod(exObj(exProp("text", exStr())), exInt()))},
		}))
	return sessionInfo, notes
}

// exRelayWorker is the worker process of the relay example. 1. Its link first; 2. its host at its address: connect
// first (W6), then load the core facet with the start data, then the plugins; the server closing the link ends it.
func exRelayWorker(id string) {
	sessionInfo, notes := exRelayContracts()
	port := newExFramedPort(os.Stdin, os.Stdout)
	workerLink := link.IpcLink(port)
	port.start()
	address := Session.Address(jsonObject("id", id))
	worker, err := CreateHost(HostOptions{Address: &address, Principal: workerPrincipal(id), OnError: func(err HostError) {
		fmt.Fprintln(os.Stderr, "worker error:", err.Kind, err.Error)
		os.Exit(1)
	}})
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker host:", err)
		os.Exit(1)
	}
	if err := worker.Connect(workerLink, LinkOptions{Principal: serverPrincipal(), Trusted: true}, background); err != nil {
		fmt.Fprintln(os.Stderr, "worker connect:", err)
		os.Exit(1)
	}
	init := jsonObject("id", id, "title", "Session "+id, "pid", float64(os.Getpid()))
	core := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(sessionInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"init": exConstant(init)}, nil), nil
		}, ServeOptions{})
	})
	doc := exStringListDoc("notes")
	notesFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			notesDoc := stateOf(self, doc)
			info := self.Get(sessionInfo, jsonObject())
			return exVariant("owner", map[string]chord.Watchable[delta.JsonValue]{
				"info": Expose(info.Member("init")),
				"notes": self.Derive([]any{notesDoc.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return exAdd(notesDoc, args.Value("text"), ctx)
				}),
			}), nil
		}, ServeOptions{})
	})
	if _, err := worker.Load([]HostFacet{{Name: "@example/core", Facet: core}, {Name: "@example/notes", Facet: notesFacet}}, background); err != nil {
		fmt.Fprintln(os.Stderr, "worker load:", err)
		os.Exit(1)
	}
	closed := make(chan struct{})
	workerLink.OnClose(func() { close(closed) })
	<-closed
	os.Exit(0)
}

func TestExample13Relay(t *testing.T) {
	if id := os.Getenv(exRelayWorkerEnv); id != "" {
		exRelayWorker(id)
		return
	}
	_, notes := exRelayContracts()
	serverErrors := &hostErrors{}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: serverErrors.push})
	var children []*exec.Cmd
	var childrenMu sync.Mutex
	// 3. A route forks a worker per session and returns the IPC link at once.
	mustRoute(t)(server.Route(Session, RouteOptions{
		Authorize: func(principal data.Principal, _ contracts.Address) (bool, error) {
			return principalKind(principal) == "owner", nil
		},
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			id, _ := address.Params.Value("id").(string)
			child := exec.Command(os.Args[0], "-test.run=^TestExample13Relay$")
			child.Env = append(os.Environ(), exRelayWorkerEnv+"="+id)
			stdin, err := child.StdinPipe()
			if err != nil {
				return ResolvedLink{}, err
			}
			stdout, err := child.StdoutPipe()
			if err != nil {
				return ResolvedLink{}, err
			}
			child.Stderr = os.Stderr
			if err := child.Start(); err != nil {
				return ResolvedLink{}, err
			}
			childrenMu.Lock()
			children = append(children, child)
			childrenMu.Unlock()
			port := newExFramedPort(stdout, stdin)
			made := link.IpcLink(port)
			port.start()
			return ResolvedLink{Link: made, Principal: workerPrincipal(id), Trusted: true}, nil
		},
	}, background))
	mustLoad(t)(server.Load(nil, background))
	// 4. Tabs reach the server over a WebSocket; its entry point stamps each as the owner here.
	upgrader := websocket.Upgrader{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		socket := newExSocket(conn)
		must(t, server.Accept(link.WebSocketLink(socket), LinkOptions{Principal: ownerPrincipal()}, background))
		socket.start()
	}))
	defer httpServer.Close()
	// 5. A tab over a WebSocket of its own: its holds go tab -> server (relay) -> worker.
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	must(t, err)
	must(t, resp.Body.Close())
	tabSocket := newExSocket(conn)
	tabErrors := &hostErrors{}
	tab := newHost(t, HostOptions{OnError: tabErrors.push})
	must(t, tab.Connect(link.WebSocketLink(tabSocket), LinkOptions{Principal: serverPrincipal()}, background))
	tabSocket.start()
	mustLoad(t)(tab.Load(nil, background))
	s1 := exGet(t, tab, notes, jsonObject("session", "S1"))
	info := exValue(t, s1, "info").(*delta.JsonObject)
	if info.Value("id") != "S1" || info.Value("title") != "Session S1" || info.Value("pid") == float64(os.Getpid()) || info.Value("pid").(float64) <= 0 {
		t.Fatalf("tab: S1 info = %s (server pid %d)", exText(info), os.Getpid())
	}
	exCall(t, s1, "add", jsonObject("text", "milk"))
	exCall(t, s1, "add", jsonObject("text", "bread"))
	scopeEventually(t, "tab: S1 notes", func() bool { return exJSONEqual(exValue(t, s1, "notes"), exJSON(t, `["milk","bread"]`)) })
	// 6. Another session is another worker process.
	s2 := exGet(t, tab, notes, jsonObject("session", "S2"))
	info2 := exValue(t, s2, "info").(*delta.JsonObject)
	if info2.Value("id") != "S2" || info2.Value("pid") == info.Value("pid") {
		t.Fatalf("tab: S2 info = %s", exText(info2))
	}
	exEqual(t, "tab: S2 notes", exValue(t, s2, "notes"), exJSON(t, `[]`))
	// 7. Closing the server closes its route links; each worker exits when its link closes.
	exCloseHost(t, tab)
	exCloseHost(t, server)
	childrenMu.Lock()
	defer childrenMu.Unlock()
	if len(children) != 2 {
		t.Fatalf("forked %d workers, want 2", len(children))
	}
	for _, child := range children {
		if err := child.Wait(); err != nil {
			t.Fatalf("worker exit: %v", err)
		}
	}
	if serverErrors.Len() > 0 || tabErrors.Len() > 0 {
		t.Fatalf("background errors: %v %v", serverErrors.All(), tabErrors.All())
	}
}
