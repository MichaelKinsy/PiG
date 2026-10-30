package host

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/bench/relay.ts: light benchmarks of links and relays (slice 4a): commit-to-frame and
// wire bytes through a relay (tab <-> server <-> worker) over memory links, an IPC link to a worker and a loopback
// WebSocket to the tab; hold-to-live through a relay including the `resolve` of a worker; a tab throttled to 64 KiB/s
// with stalls of 1, 10 and 60 s receiving a streamed reply into a 500 KB view, directly and through a relay (the worker
// folds either way: its window is per origin), in virtual time; 10,000 calls from one tab with another tab's latency
// through the relay. Pi prints markdown tables; the Go form is one benchmark per table, a sub-benchmark per row, the
// row's columns as metrics.
//
// Go forms of the transports: Pi forks a child process and talks Node's IPC channel (`serialization: "advanced"`) and
// serves a WebSocket with `ws`; Go has no fork of a running test and no IPC channel, so the "forked worker" is a worker
// host on a goroutine of its own behind an IpcLink over a loopback TCP socket with length-prefixed frames (as
// chord/next/link's IPC test plays Node's channel), and the WebSocket is gorilla/websocket over httptest. "First hold
// to live, the fork included" therefore measures the socket and the worker host, not a process.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkRelay -benchtime=1x ./chord/next/host/

var (
	benchRelayEntry = contracts.Obj(
		contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str()),
		contracts.Prop("author", contracts.Str()), contracts.Prop("at", contracts.Int()))
	benchRelayShape = contracts.Obj(contracts.Prop("entries", contracts.Arr(benchRelayEntry)))
)

// benchRelayStore is a worker's own store: one document of entries, and the write the benchmark makes (a streamed token).
var benchRelayStore = mustContract(contracts.Local("bench.store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("doc", benchRelayShape), contracts.Mem("stream", contracts.NewMethod(contracts.Str()))},
}))

// benchRelayView is what a tab holds: its session's store as it is, placed on the session's worker, and a small call.
var benchRelayView = mustContract(contracts.Remote("bench.view", contracts.RemoteDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
		return Session.Address(jsonObject("id", params.Value("session"))), nil
	},
	Variants: []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{
		State: []contracts.Property{contracts.Prop("doc", benchRelayShape)},
		Methods: []contracts.MethodEntry{
			contracts.Meth("stream", contracts.NewMethod(contracts.Str())),
			contracts.Meth("ping", contracts.NewMethod(contracts.Int(), contracts.Int())),
		},
	})},
}))

// relayStart is what a worker gets in its start message: its session and the payload its store begins with.
type relayStart struct {
	id      string
	entries string
}

// relayEntries are the notes list (1,000 entries of 80 characters, 134 KiB) or a 500 KB conversation document (500 of 960).
func relayEntries(kind string) []any {
	if kind == "notes" {
		entries := make([]any, 1_000)
		for id := range entries {
			entries[id] = benchLinkEntry(id, 80)
		}
		return entries
	}
	entries := make([]any, 500)
	for id := range entries {
		entries[id] = benchLinkEntry(id, 960)
	}
	return entries
}

// relayWorkerFacet is a worker's facet: its store starts with the start message's entries (the §18 core facet pattern).
func relayWorkerFacet(start relayStart) func(host FacetHost, _ FacetSelf) error {
	doc, err := documents.DefineState("doc", documents.StateOptions{
		Schema:  benchRelayShape,
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("entries", relayEntries(start.entries)) },
	})
	if err != nil {
		panic(err)
	}
	return func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(benchRelayStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			state := self.State(doc).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"doc": Expose(state)},
				Methods: map[string]contracts.MethodFunction{
					"stream": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := state.Change(func(draft *delta.Object) (any, error) {
							entries := draft.Array("entries")
							last := entries.Object(entries.Len() - 1)
							return nil, last.Set("text", last.Get("text").(string)+args.(string))
						}, ctx)
						return nil, err
					},
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(benchRelayView, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			store := self.Get(benchRelayStore, jsonObject())
			return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"doc": Expose(store.Member("doc"))},
				Methods: map[string]contracts.MethodFunction{
					"stream": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return store.Call("stream", args, ctx)
					},
					"ping": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return args.(float64) + 1, nil },
				},
			}}, nil
		}, ServeOptions{})
	}
}

// relayWorkerHost is a worker host for start, not loaded yet.
func relayWorkerHost(b testing.TB, start relayStart) Host {
	address := Session.Address(jsonObject("id", start.id))
	return newHost(b, HostOptions{
		Address:   &address,
		Principal: workerPrincipal(start.id),
		OnError:   func(err HostError) { b.Errorf("onError %s", err.Kind) },
	})
}

// startRelayWorker is a worker on end, its parent link: connect first, then load (W6).
func startRelayWorker(b testing.TB, start relayStart, end link.Link) Host {
	worker := relayWorkerHost(b, start)
	must(b, worker.Connect(end, LinkOptions{Principal: serverPrincipal(), Trusted: true, Codec: link.BinaryCodec}, background))
	go func() {
		_ = worker.Load([]HostFacet{{Name: "@bench/worker", Facet: DefineFacet(relayWorkerFacet(start))}}, background)
	}()
	return worker
}

// timedLink is a link end timing how long its host handles each received message.
type timedLink struct {
	link.Link
	mu      sync.Mutex
	longest time.Duration
}

func (t *timedLink) OnMessage(listener func(message []byte)) {
	t.Link.OnMessage(func(message []byte) {
		start := time.Now()
		listener(message)
		elapsed := time.Since(start)
		t.mu.Lock()
		t.longest = max(t.longest, elapsed)
		t.mu.Unlock()
	})
}

func (t *timedLink) reset() {
	t.mu.Lock()
	t.longest = 0
	t.mu.Unlock()
}

func (t *timedLink) longestHandling() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.longest
}

// ipcFramedPort is a link.IpcPort over a socket: length-prefixed frames, a leading 0x00 marks a message of the link.
type ipcFramedPort struct {
	conn net.Conn
	mu   sync.Mutex
	wmu  sync.Mutex

	connected   bool
	messages    []func(any)
	disconnects []func()
}

func newIpcFramedPort(conn net.Conn) *ipcFramedPort {
	port := &ipcFramedPort{conn: conn, connected: true}
	go port.read()
	return port
}

func (port *ipcFramedPort) read() {
	for {
		var length [4]byte
		if _, err := io.ReadFull(port.conn, length[:]); err != nil {
			port.ended()
			return
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(port.conn, frame); err != nil {
			port.ended()
			return
		}
		port.mu.Lock()
		listeners := slices.Clone(port.messages)
		port.mu.Unlock()
		for _, listener := range listeners {
			if frame[0] == 0 {
				listener(frame[1:])
			}
		}
	}
}

func (port *ipcFramedPort) ended() {
	port.mu.Lock()
	wasConnected := port.connected
	port.connected = false
	listeners := slices.Clone(port.disconnects)
	port.mu.Unlock()
	if wasConnected {
		for _, listener := range listeners {
			listener()
		}
	}
}

func (port *ipcFramedPort) Send(message []byte, callback func(error)) (bool, error) {
	frame := make([]byte, 4, 5+len(message))
	binary.BigEndian.PutUint32(frame, uint32(1+len(message)))
	frame = append(append(frame, 0), message...)
	port.wmu.Lock()
	_, err := port.conn.Write(frame)
	port.wmu.Unlock()
	callback(err)
	return err == nil, nil
}

func (port *ipcFramedPort) OnMessage(listener func(message any)) func() {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.messages = append(port.messages, listener)
	return func() {}
}

func (port *ipcFramedPort) OnDisconnect(listener func()) func() {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.disconnects = append(port.disconnects, listener)
	return func() {}
}

func (port *ipcFramedPort) Disconnect() {
	port.mu.Lock()
	port.connected = false
	port.mu.Unlock()
	_ = port.conn.Close()
}

func (port *ipcFramedPort) Connected() bool {
	port.mu.Lock()
	defer port.mu.Unlock()
	return port.connected
}

// ipcPair is the two ports of one loopback socket.
func ipcPair(b testing.TB) (*ipcFramedPort, *ipcFramedPort) {
	b.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(b, err)
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := listener.Accept()
		accepted <- conn
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	must(b, err)
	return newIpcFramedPort(client), newIpcFramedPort(<-accepted)
}

// relayWorkerKind is how a server reaches its workers: in this process, or over an IPC link to a worker host of its own.
type relayWorkerKind string

const (
	relayMemory relayWorkerKind = "memory"
	relayIPC    relayWorkerKind = "ipc"
)

// benchRelayServer is a server routing `Session` to workers of kind whose stores start with entries.
type benchRelayServer struct {
	host Host
	mu   sync.Mutex
	// workerLinks are each worker link's server end, counted.
	workerLinks []*countedLink
	// workers are the in-process workers and their ends, timed.
	workers []benchRelayWorker
	// ends are the memory ends the server's links own (for settling).
	ends []*link.MemoryLink
}

type benchRelayWorker struct {
	host Host
	end  *timedLink
}

func benchRelayServerOf(b testing.TB, kind relayWorkerKind, entries string) *benchRelayServer {
	b.Helper()
	server := &benchRelayServer{}
	server.host = newHost(b, HostOptions{
		Address: new(Server.Address(nil)), Principal: serverPrincipal(),
		OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) },
	})
	must(b, server.host.Route(Session, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(address contracts.Address, _ context.Context) (ResolvedLink, error) {
			start := relayStart{id: address.Params.Value("id").(string), entries: entries}
			principal := workerPrincipal(start.id)
			var resolved link.Link
			if kind == relayMemory {
				a, c := link.MemoryLinkPair()
				end := &timedLink{Link: c}
				worker := startRelayWorker(b, start, end)
				counted := newCountedLink(a)
				server.mu.Lock()
				server.workers = append(server.workers, benchRelayWorker{host: worker, end: end})
				server.workerLinks = append(server.workerLinks, counted)
				server.ends = append(server.ends, a)
				server.mu.Unlock()
				resolved = counted
			} else {
				parent, child := ipcPair(b)
				startRelayWorker(b, start, link.IpcLink(child))
				counted := newCountedLink(link.IpcLink(parent))
				server.mu.Lock()
				server.workerLinks = append(server.workerLinks, counted)
				server.mu.Unlock()
				resolved = counted
			}
			return ResolvedLink{Link: resolved, Principal: principal, Trusted: true, Codec: link.BinaryCodec}, nil
		},
	}, background))
	must(b, server.host.Load(nil, background))
	return server
}

// benchRelayTab is a client host connected to end.
func benchRelayTab(b testing.TB, end link.Link) Host {
	b.Helper()
	host := newHost(b, HostOptions{OnError: func(err HostError) { b.Errorf("onError %s", err.Kind) }})
	must(b, host.Connect(end, LinkOptions{Principal: serverPrincipal(), Codec: link.BinaryCodec}, background))
	must(b, host.Load(nil, background))
	return host
}

// benchRelayMemoryTab is a tab on `on` over a memory link, the server's end counted.
func benchRelayMemoryTab(b testing.TB, on *benchRelayServer) (Host, *countedLink, *link.MemoryLink) {
	b.Helper()
	a, c := link.MemoryLinkPair()
	counted := newCountedLink(a)
	must(b, on.host.Accept(counted, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
	return benchRelayTab(b, c), counted, a
}

// gorillaSocket is a link.WebSocketLike over a gorilla/websocket connection, open already.
type gorillaSocket struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	wmu       sync.Mutex
	listeners map[link.WebSocketEvent][]func(any)
	state     int
}

func newGorillaSocket(conn *websocket.Conn) *gorillaSocket {
	socket := &gorillaSocket{conn: conn, listeners: map[link.WebSocketEvent][]func(any){}, state: 1}
	return socket
}

// start begins reading frames; it runs once the link listens.
func (s *gorillaSocket) start() {
	go func() {
		for {
			kind, frame, err := s.conn.ReadMessage()
			if err != nil {
				s.mu.Lock()
				s.state = 3
				closes := slices.Clone(s.listeners[link.WebSocketClose])
				s.mu.Unlock()
				for _, listener := range closes {
					listener(nil)
				}
				return
			}
			var data any = frame
			if kind != websocket.BinaryMessage {
				data = string(frame)
			}
			s.mu.Lock()
			messages := slices.Clone(s.listeners[link.WebSocketMessage])
			s.mu.Unlock()
			for _, listener := range messages {
				listener(data)
			}
		}
	}()
}

func (s *gorillaSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *gorillaSocket) SetBinaryType(string) {}

func (s *gorillaSocket) Send(frame []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (s *gorillaSocket) Close() { _ = s.conn.Close() }

func (s *gorillaSocket) AddEventListener(event link.WebSocketEvent, listener func(data any)) func() {
	s.mu.Lock()
	s.listeners[event] = append(s.listeners[event], listener)
	s.mu.Unlock()
	return func() {}
}

// benchRelayWebSocketTab is a tab on `on` over a loopback WebSocket (gorilla serving and dialling), the server's end
// counted.
func benchRelayWebSocketTab(b testing.TB, on *benchRelayServer) (Host, *countedLink, func()) {
	b.Helper()
	upgrader := websocket.Upgrader{}
	accepted := make(chan *countedLink, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		socket := newGorillaSocket(conn)
		counted := newCountedLink(link.WebSocketLink(socket))
		_ = on.host.Accept(counted, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background)
		socket.start()
		accepted <- counted
	}))
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	must(b, err)
	must(b, resp.Body.Close())
	socket := newGorillaSocket(conn)
	end := link.WebSocketLink(socket)
	host := benchRelayTab(b, end)
	socket.start()
	return host, <-accepted, httpServer.Close
}

// benchRelayDirect is a worker with entries served directly to a tab over one memory link (no relay), the worker's end
// counted.
type benchRelayDirect struct {
	worker Host
	link   *countedLink
	end    *timedLink
	tab    Host
	ends   []*link.MemoryLink
}

func benchRelayDirectOf(b testing.TB, entries string, wrap func(link.Link) link.Link) *benchRelayDirect {
	b.Helper()
	start := relayStart{id: "D", entries: entries}
	worker := relayWorkerHost(b, start)
	must(b, worker.Load([]HostFacet{{Name: "@bench/worker", Facet: DefineFacet(relayWorkerFacet(start))}}, background))
	a, c := link.MemoryLinkPair()
	counted := newCountedLink(a)
	end := &timedLink{Link: counted}
	must(b, worker.Accept(end, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
	var tabLink link.Link = c
	if wrap != nil {
		tabLink = wrap(c)
	}
	return &benchRelayDirect{worker: worker, link: counted, end: end, tab: benchRelayTab(b, tabLink), ends: []*link.MemoryLink{a}}
}

func (d *benchRelayDirect) close(b testing.TB) {
	must(b, d.tab.Close(background))
	must(b, d.worker.Close(background))
}

// relayCommits are the commits as a tab sees them: the tab calls `stream` on its view, each one commit on the worker; the
// time from the call to the tab's watch frame, and the bytes per commit on the tab link (sent to the tab) and the worker
// link (received from the worker).
func relayCommits(b *testing.B, holder Host, session string, tabLink *countedLink, workerLinks func() []*countedLink) {
	b.Helper()
	view, err := holder.Get(benchRelayView, jsonObject("session", session), background, GetOptions{})
	must(b, err)
	// Made by the route's `resolve` for the first hold (none without a relay).
	var workerLink *countedLink
	if links := workerLinks(); len(links) > 0 {
		workerLink = links[0]
	}
	arrived, stop := benchFrames2(b, view)
	defer stop()
	// Warm-up.
	for range 100 {
		_, err := view.Call("stream", " w", background)
		must(b, err)
		<-arrived
	}
	tab0, _ := tabLink.sent()
	var worker0 int
	if workerLink != nil {
		worker0 = workerLink.received()
	}
	b.ReportAllocs()
	runs := 0
	for b.Loop() {
		if _, err := view.Call("stream", " tok", background); err != nil {
			b.Fatal(err)
		}
		<-arrived
		runs++
	}
	tab1, _ := tabLink.sent()
	b.ReportMetric(float64(tab1-tab0)/float64(max(runs, 1)), "wire-B/commit-to-tab")
	if workerLink != nil {
		b.ReportMetric(float64(workerLink.received()-worker0)/float64(max(runs, 1)), "wire-B/commit-from-worker")
	}
	view.Release()
}

// benchFrames2 is benchFrames for a view of the relay benchmarks.
func benchFrames2(b testing.TB, ref *Reference) (chan struct{}, func()) {
	b.Helper()
	watch, err := ref.Member("doc").Watch(background)
	must(b, err)
	arrived := make(chan struct{}, 1<<16)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		arrived <- struct{}{}
		return nil
	})
	return arrived, func() { _, _ = watch.Stop() }
}

// BenchmarkRelayCommits is "Commits through a relay (1,000 streamed tokens into the 1,000-item notes list view)": commit
// through a relay over each transport, and directly (one memory link, no relay) as the baseline.
func BenchmarkRelayCommits(b *testing.B) {
	b.Run("memory tab, in-process worker (memory)", func(b *testing.B) {
		server := benchRelayServerOf(b, relayMemory, "notes")
		tab, tabLink, _ := benchRelayMemoryTab(b, server)
		relayCommits(b, tab, "C", tabLink, server.links)
		must(b, tab.Close(background))
		must(b, server.host.Close(background))
	})
	b.Run("memory tab, forked worker (IPC)", func(b *testing.B) {
		server := benchRelayServerOf(b, relayIPC, "notes")
		tab, tabLink, _ := benchRelayMemoryTab(b, server)
		relayCommits(b, tab, "C", tabLink, server.links)
		must(b, tab.Close(background))
		must(b, server.host.Close(background))
	})
	b.Run("first hold to live, the worker started over IPC (one sample)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			b.StopTimer()
			server := benchRelayServerOf(b, relayIPC, "notes")
			tab, _, _ := benchRelayMemoryTab(b, server)
			b.StartTimer()
			view, err := tab.Get(benchRelayView, jsonObject("session", "C"+strconv.Itoa(i)), background, GetOptions{})
			b.StopTimer()
			must(b, err)
			view.Release()
			must(b, tab.Close(background))
			must(b, server.host.Close(background))
			b.StartTimer()
		}
	})
	b.Run("loopback WebSocket tab, forked worker (IPC)", func(b *testing.B) {
		server := benchRelayServerOf(b, relayIPC, "notes")
		tab, tabLink, closeServer := benchRelayWebSocketTab(b, server)
		relayCommits(b, tab, "C", tabLink, server.links)
		must(b, tab.Close(background))
		must(b, server.host.Close(background))
		closeServer()
	})
	b.Run("no relay: memory tab on the worker (baseline)", func(b *testing.B) {
		direct := benchRelayDirectOf(b, "notes", nil)
		relayCommits(b, direct.tab, "D", direct.link, func() []*countedLink { return nil })
		direct.close(b)
	})
}

func (s *benchRelayServer) links() []*countedLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*countedLink(nil), s.workerLinks...)
}

// BenchmarkRelayHolds is "Hold to live, the 1,000-item notes list view (133.7 KiB snapshot)": a first hold of each of 20
// sessions (the route's `resolve` starts an in-process worker, which loads and activates the view: one hop more than a
// later hold), then holds of a resolved session, each released; and the same holds directly on a worker.
func BenchmarkRelayHolds(b *testing.B) {
	b.Run("first hold, `resolve` of an in-process worker included", func(b *testing.B) {
		server := benchRelayServerOf(b, relayMemory, "notes")
		tab, _, _ := benchRelayMemoryTab(b, server)
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := tab.Get(benchRelayView, jsonObject("session", "H"+strconv.Itoa(i)), background, GetOptions{}); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		must(b, tab.Close(background))
		must(b, server.host.Close(background))
	})
	b.Run("hold of a resolved session through the relay (four hops)", func(b *testing.B) {
		server := benchRelayServerOf(b, relayMemory, "notes")
		tab, _, _ := benchRelayMemoryTab(b, server)
		_, err := tab.Get(benchRelayView, jsonObject("session", "H0"), background, GetOptions{})
		must(b, err)
		b.ReportAllocs()
		for b.Loop() {
			view, err := tab.Get(benchRelayView, jsonObject("session", "H0"), background, GetOptions{})
			if err != nil {
				b.Fatal(err)
			}
			view.Release()
		}
		b.StopTimer()
		must(b, tab.Close(background))
		must(b, server.host.Close(background))
	})
	b.Run("no relay: hold on the worker (two hops, baseline)", func(b *testing.B) {
		direct := benchRelayDirectOf(b, "notes", nil)
		_, err := direct.tab.Get(benchRelayView, jsonObject("session", "D"), background, GetOptions{})
		must(b, err)
		b.ReportAllocs()
		for b.Loop() {
			view, err := direct.tab.Get(benchRelayView, jsonObject("session", "D"), background, GetOptions{})
			if err != nil {
				b.Fatal(err)
			}
			view.Release()
		}
		b.StopTimer()
		direct.close(b)
	})
}

// throttledLink is a tab's end of a link that delivers what arrives at rate bytes per virtual second, whole messages in
// order, and nothing while stalled; virtual time advances by step. Idle time banks no budget.
type throttledLink struct {
	link.Link
	rate float64

	mu        sync.Mutex
	stalled   bool
	delivered int
	queue     [][]byte
	budget    float64
	listener  func(message []byte)
}

func newThrottledLink(end link.Link, rate float64) *throttledLink {
	t := &throttledLink{Link: end, rate: rate}
	end.OnMessage(func(message []byte) {
		t.mu.Lock()
		t.queue = append(t.queue, message)
		t.mu.Unlock()
	})
	return t
}

func (t *throttledLink) OnMessage(listener func(message []byte)) {
	t.mu.Lock()
	t.listener = listener
	t.mu.Unlock()
}

func (t *throttledLink) setStalled(stalled bool) {
	t.mu.Lock()
	t.stalled = stalled
	t.mu.Unlock()
}

func (t *throttledLink) deliveredBytes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.delivered
}

// step: milliseconds of virtual time pass.
func (t *throttledLink) step(milliseconds float64) {
	t.mu.Lock()
	if t.stalled {
		t.mu.Unlock()
		return
	}
	budget := t.rate * milliseconds / 1000
	t.budget += budget
	var deliver [][]byte
	for len(t.queue) > 0 && float64(len(t.queue[0])) <= t.budget {
		message := t.queue[0]
		t.queue = t.queue[1:]
		t.budget -= float64(len(message))
		t.delivered += len(message)
		deliver = append(deliver, message)
	}
	if len(t.queue) == 0 {
		t.budget = min(t.budget, budget)
	}
	listener := t.listener
	t.mu.Unlock()
	for _, message := range deliver {
		listener(message)
	}
}

const (
	relayStep = 10 // virtual milliseconds per step
	// relayTokens are tokens per virtual second: a fast model's streamed reply.
	relayTokens = 100
)

// relayPause is a pause of the throttled tab: a stall (it receives nothing and so acknowledges nothing), or the same time
// without one (the control: what the streaming alone costs).
type relayPause struct {
	stall   bool
	seconds int
}

// relayThrottled is a tab throttled to 64 KiB/s holding the 500 KB document view of a worker that streams a reply at 100
// tokens/s, in virtual time (10 ms steps): 2 s of streaming, the pause, 10 s more of streaming, then until the tab caught
// up. relayed: through a server (it forwards at once, the worker folds the tab origin's ops); direct: on the worker (it
// folds the ops in its pending queues).
func relayThrottled(b *testing.B, relayed bool, pause relayPause) {
	b.Helper()
	var throttle *throttledLink
	if relayed {
		server := benchRelayServerOf(b, relayMemory, "document")
		a, c := link.MemoryLinkPair()
		must(b, server.host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
		throttle = newThrottledLink(c, 64*1024)
		tab := benchRelayTab(b, throttle)
		defer func() {
			must(b, tab.Close(background))
			must(b, server.host.Close(background))
		}()
		settle := func() {
			server.mu.Lock()
			ends := append([]*link.MemoryLink{a}, server.ends...)
			server.mu.Unlock()
			for range 3 {
				transported(ends...)
			}
		}
		producerOf := func() (Host, *timedLink) {
			server.mu.Lock()
			defer server.mu.Unlock()
			return server.workers[0].host, server.workers[0].end
		}
		runThrottled(b, relayed, pause, throttle, tab, producerOf, settle)
		return
	}
	direct := benchRelayDirectOf(b, "document", func(end link.Link) link.Link {
		throttle = newThrottledLink(end, 64*1024)
		return throttle
	})
	defer direct.close(b)
	settle := func() {
		for range 3 {
			transported(direct.ends...)
		}
	}
	runThrottled(b, relayed, pause, throttle, direct.tab, func() (Host, *timedLink) { return direct.worker, direct.end }, settle)
}

func runThrottled(b *testing.B, relayed bool, pause relayPause, throttle *throttledLink, tab Host, producerOf func() (Host, *timedLink), settle func()) {
	b.Helper()
	session := "D"
	if relayed {
		session = "T"
	}
	// The first snapshot crosses the throttle in virtual time too.
	got := asyncGet(tab, benchRelayView, jsonObject("session", session), background, GetOptions{})
	var view *Reference
	for view == nil {
		throttle.step(relayStep)
		settle()
		select {
		case result := <-got:
			must(b, result.err)
			view = result.ref
		default:
		}
	}
	worker, providerEnd := producerOf()
	store, err := worker.Get(benchRelayStore, jsonObject(), background, GetOptions{})
	must(b, err)
	last := func(ref *Reference) int {
		value, err := ref.Member("doc").Value()
		must(b, err)
		entries := value.(*delta.JsonObject).Value("entries").([]any)
		return len(entries[len(entries)-1].(*delta.JsonObject).Value("text").(string))
	}
	caughtUp := func() bool { return last(view) == last(store) }
	clock := 0
	var outside, during time.Duration
	var outsideCount, duringCount int
	// step is one virtual step: a token when streaming (its commit timed), delivery, the hosts' turn.
	step := func(streaming, inPause bool) {
		clock += relayStep
		if streaming && clock%(1000/relayTokens) == 0 {
			start := time.Now()
			_, err := store.Call("stream", " tok", background)
			must(b, err)
			if inPause {
				during += time.Since(start)
				duringCount++
			} else {
				outside += time.Since(start)
				outsideCount++
			}
		}
		throttle.step(relayStep)
		settle()
	}
	b.ReportAllocs()
	if b.Loop() {
		for range 2000 / relayStep {
			step(true, false)
		}
		for !caughtUp() {
			step(false, false)
		}
		heap0 := benchSettledHeap()
		throttle.setStalled(pause.stall)
		for range pause.seconds * 1000 / relayStep {
			step(true, true)
		}
		heap := int64(benchSettledHeap()) - int64(heap0)
		throttle.setStalled(false)
		stallEnd := clock
		delivered0 := throttle.deliveredBytes()
		providerEnd.reset()
		recovered := -1
		for i := 0; recovered < 0 && i < 120_000/relayStep; i++ {
			step(clock-stallEnd < 10_000, false)
			if caughtUp() {
				recovered = clock - stallEnd
			}
		}
		b.ReportMetric(float64(recovered)/1000, "s-to-catch-up")
		b.ReportMetric(float64(throttle.deliveredBytes()-delivered0), "wire-B-until-then")
		b.ReportMetric(float64(heap)/1024, "heap-KiB-during-pause")
		b.ReportMetric(float64(outside.Microseconds())/float64(max(outsideCount, 1)), "commit-us-outside")
		b.ReportMetric(float64(during.Microseconds())/float64(max(duringCount, 1)), "commit-us-during")
		b.ReportMetric(float64(providerEnd.longestHandling().Microseconds()), "longest-handling-us")
	}
	store.Release()
	view.Release()
}

// BenchmarkRelaySlowTabs is "A tab throttled to 64 KiB/s, a reply streamed at 100 tokens/s into the 500 KB view (virtual
// time)": direct and relayed, each with stalls of 1, 10 and 60 s and a 60 s control without one.
func BenchmarkRelaySlowTabs(b *testing.B) {
	for _, relayed := range []bool{false, true} {
		topology := "direct"
		if relayed {
			topology = "relayed"
		}
		for _, seconds := range []int{1, 10, 60} {
			b.Run(topology+", "+strconv.Itoa(seconds)+" s stall", func(b *testing.B) {
				relayThrottled(b, relayed, relayPause{stall: true, seconds: seconds})
			})
		}
		b.Run(topology+", 60 s without a stall (control)", func(b *testing.B) {
			relayThrottled(b, relayed, relayPause{stall: false, seconds: 60})
		})
	}
}

// BenchmarkRelayCalls is "Calls through the relay (in-process worker, memory links, N = 256 per link)": 10,000 calls from
// tab A through the relay at once, and tab B's sequential calls meanwhile; B alone as the baseline.
func BenchmarkRelayCalls(b *testing.B) {
	server := benchRelayServerOf(b, relayMemory, "notes")
	aTab, _, _ := benchRelayMemoryTab(b, server)
	bTab, _, _ := benchRelayMemoryTab(b, server)
	viewA, err := aTab.Get(benchRelayView, jsonObject("session", "K"), background, GetOptions{})
	must(b, err)
	viewB, err := bTab.Get(benchRelayView, jsonObject("session", "K"), background, GetOptions{})
	must(b, err)
	b.Cleanup(func() {
		must(b, aTab.Close(background))
		must(b, bTab.Close(background))
		must(b, server.host.Close(background))
	})
	latencies := func(until func() bool, limit int) []time.Duration {
		var times []time.Duration
		for !until() && len(times) < limit {
			start := time.Now()
			if _, err := viewB.Call("ping", float64(len(times)), background); err != nil {
				b.Fatal(err)
			}
			times = append(times, time.Since(start))
		}
		slices.Sort(times)
		return times
	}
	percentile := func(times []time.Duration, p float64) float64 {
		return float64(times[min(len(times)-1, int(float64(len(times))*p))].Microseconds())
	}
	for i := range 1_000 {
		if _, err := viewB.Call("ping", float64(i), background); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("tab B alone", func(b *testing.B) {
		alone := latencies(func() bool { return false }, 1_000)
		b.ReportMetric(percentile(alone, 0.5), "p50-us")
		b.ReportMetric(percentile(alone, 0.95), "p95-us")
		b.ReportMetric(float64(alone[len(alone)-1].Microseconds()), "max-us")
		b.ReportMetric(float64(len(alone)), "calls")
	})
	b.Run("tab B while tab A has 10,000 calls in flight or queued; tab A until the last result", func(b *testing.B) {
		if b.Loop() {
			var done sync.WaitGroup
			finished := make(chan struct{})
			start := time.Now()
			results := make([]*parkedCallResult, 10_000)
			for i := range results {
				results[i] = startCall(b, viewA, "ping", float64(i), background)
			}
			done.Go(func() {
				for _, result := range results {
					if _, err := result.wait(); err != nil {
						b.Error(err)
					}
				}
				close(finished)
			})
			during := latencies(func() bool {
				select {
				case <-finished:
					return true
				default:
					return false
				}
			}, 100_000)
			done.Wait()
			total := time.Since(start)
			if len(during) > 0 {
				b.ReportMetric(percentile(during, 0.5), "p50-us")
				b.ReportMetric(percentile(during, 0.95), "p95-us")
				b.ReportMetric(float64(during[len(during)-1].Microseconds()), "max-us")
				b.ReportMetric(float64(len(during)), "calls")
			}
			b.ReportMetric(float64(total.Microseconds()), "tab-A-total-us")
		}
	})
	runtime.KeepAlive(viewA)
}
