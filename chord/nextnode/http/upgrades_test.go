package http

// pi: packages/chord/src/next-node/http/upgrades.ts

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Tests of upgraded connections (slice 10 §2, D6, Q6; decisions "allowed hosts only through contributions", "plugin sockets get
// a heartbeat"): the links endpoint (`host.accept`, options per socket, refused sockets, the 1 MiB frame default, the output
// cap), plugin sockets (handed to their handler, an oversized frame closing only that socket, a throwing handler),
// connections terminated at once when their socket handler or host leaves (a raw peer can act no more), an upgrade checked
// again after authenticating, the heartbeat, close bounded for a peer that never answers, and nothing kept of a closed
// socket. Ports packages/chord/test/next-node/http/upgrades.test.ts.
//
// The revocation tests drive the router with `host.Reload` as Pi's do. A router change reaches the connections on a task of its own
// (the router change is applied on the connection's own task), where Pi's watch listener runs before `reload` resolves: after a reload the tests wait for
// what Pi's checks at once (a scope's seal, a connection ended by the server) before they check or write.
//

var serverKind = must(contracts.NewHostKind("server"))

// who is the remote type served at the server's address: who the server sees calling.
var who = must(contracts.Remote("test.who", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverKind.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{contracts.Meth("who", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
	})},
}))

var serveWho = host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
	return h.Serve(who, func(_ *delta.JsonObject, _ data.Principal, _ host.Self) (contracts.Implementation, error) {
		return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"who": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				principal := ctx.(host.Ctx).Principal()
				if principal == nil {
					return "null", nil
				}
				out, err := chordjson.Marshal(principal)
				return string(out), err
			},
		}}}, nil
	}, host.ServeOptions{})
})

// kindOf is the kind of a principal.
func kindOf(principal data.Principal) string {
	if principal == nil {
		return "nobody"
	}
	kind, _ := principal.Value("kind").(string)
	return kind
}

// echoSocket is a plugin socket that echoes text, with its handler's request params and principal sent first.
var echoSocket = &HttpSocket{Handle: func(socket *WebSocketHandle, request HttpRequest, ctx host.Ctx) error {
	socket.Send([]byte(request.Params()["room"]+" as "+kindOf(ctx.Principal())), false, nil)
	socket.OnMessage(func(message Message) { socket.Send(message.Data, false, nil) })
	return nil
}}

// echo is the plugin contributing echoSocket under `/echo/:room`, frames of at most 64 bytes.
func echo() plugin {
	return plugin{"@app/echo", contributing(HttpContribution{Sockets: []HttpSocketEntry{{Pattern: "/echo/:room", Handler: echoSocket}}, MaxPayload: 64})}
}

var owner = map[string]string{"x-principal": "owner"}

func withHeaders(base map[string]string, extra map[string]string) map[string]string {
	merged := map[string]string{}
	maps.Copy(merged, base)
	maps.Copy(merged, extra)
	return merged
}

func noopSocket() *HttpSocket {
	return &HttpSocket{Handle: func(*WebSocketHandle, HttpRequest, host.Ctx) error { return nil }}
}

func mustRefused(t *testing.T, port int, path string, headers map[string]string, status int) {
	t.Helper()
	_, err := socketTo(port, path, headers, 0)
	if err == nil || err.Error() != "refused "+strconv.Itoa(status) {
		t.Fatalf("socketTo %s %v: %v, want refused %d", path, headers, err, status)
	}
}

func TestTheLinksEndpointAcceptsALinkAsTheAuthenticatedPrincipalWithTheOptionsLinksOptionsGaveThatSocket(t *testing.T) {
	var mu sync.Mutex
	var options [][2]string
	address := serverKind.Address(nil)
	server := serving(t, []plugin{{"@app/who", serveWho}}, servedOptions{
		address: &address,
		links: &LinksOptions{Path: "/chord", OptionsFunc: func(request HttpRequest, principal data.Principal) (LinkOptions, error) {
			mu.Lock()
			options = append(options, [2]string{request.Path(), kindOf(principal)})
			mu.Unlock()
			return LinkOptions{MaxPayload: 1024 * 1024}, nil
		}},
	})
	tab, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tab.Close(background) }()
	if _, err := tab.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	// The link wraps the socket at once: the server's `hello` comes with the upgrade.
	socket, err := dialSocket(server.port, "/chord", owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Connect(link.WebSocketLink(limitedSocket(socket.WebSocket)), host.LinkOptions{Principal: principalServer()}, background); err != nil {
		t.Fatal(err)
	}
	socket.Start()
	reference, err := tab.Get(background, who, delta.JsonObjectOf(), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reference.Call("who", nil, background)
	if err != nil || got != `{"kind":"owner"}` {
		t.Fatalf("who = %v, %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(options) != 1 || options[0] != [2]string{"/chord", "owner"} {
		t.Fatalf("options = %v", options)
	}
}

func TestTheLinksEndpointRefusesASocketWithoutAPrincipalFromAnotherOriginOrHostForeignOrWhoseLinksOptionsFails403(t *testing.T) {
	server := serving(t, []plugin{{"@app/hosts", contributing(HttpContribution{Hosts: []string{"a.example.com"}})}}, servedOptions{
		links: &LinksOptions{Path: "/chord", OptionsFunc: func(request HttpRequest, _ data.Principal) (LinkOptions, error) {
			if _, ok := HeaderGet(request.Headers(), "x-fail"); ok {
				return LinkOptions{}, errors.New("options")
			}
			return LinkOptions{}, nil
		}},
	})
	port := server.port
	mustRefused(t, port, "/chord", nil, 401)
	mustRefused(t, port, "/chord", withHeaders(owner, map[string]string{"origin": "http://evil.com"}), 403)
	mustRefused(t, port, "/chord", withHeaders(owner, map[string]string{"origin": "null"}), 403)
	mustRefused(t, port, "/chord", withHeaders(owner, map[string]string{"origin": "HTTP://127.0.0.1:" + strconv.Itoa(port)}), 403)
	mustRefused(t, port, "/chord", withHeaders(owner, map[string]string{"host": "evil.com"}), 403)
	mustRefused(t, port, "/chord", withHeaders(owner, map[string]string{"sec-fetch-site": "cross-site"}), 403)
	mustRefused(t, port, "/chord", withHeaders(owner, map[string]string{"x-fail": "1"}), 403)
	same := mustSocketTo(t, port, "/chord", withHeaders(owner, map[string]string{"origin": "http://127.0.0.1:" + strconv.Itoa(port)}))
	same.Close(0, "")
	errs := server.errors.all()
	if len(errs) != 1 || errs[0].Kind != ErrHookFailed || errs[0].Hook != HookLinksOptions || errs[0].Plugin != "" || errs[0].Error == nil || errs[0].Error.Error() != "options" {
		t.Fatalf("errors = %+v", errs)
	}
}

// Opus's audit H2.
func TestTheLinksEndpointTakesFramesOfAtMost1MiBUnlessLinksOptionsSaysOtherwise(t *testing.T) {
	server := serving(t, nil, servedOptions{links: &LinksOptions{Path: "/chord"}})
	socket := mustSocketTo(t, server.port, "/chord", owner)
	closed := socket.closed
	socket.Send(make([]byte, 1024*1024+1), true, nil)
	if event := receive(t, closed, "the close"); event.Code != 1009 {
		t.Fatalf("close code = %d, want 1009", event.Code)
	}
}

// fakeSocket is a socket whose buffer the test sets.
type fakeSocket struct {
	mu         sync.Mutex
	buffered   int64
	sent       []int
	terminated int
}

func (f *fakeSocket) ReadyState() int { return WebSocketOpenState }
func (f *fakeSocket) BufferedAmount() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buffered
}
func (f *fakeSocket) Send(data []byte, _ bool, _ func(error)) {
	f.mu.Lock()
	f.sent = append(f.sent, len(data))
	f.mu.Unlock()
}
func (f *fakeSocket) Terminate() {
	f.mu.Lock()
	f.terminated++
	f.mu.Unlock()
}
func (f *fakeSocket) Close(int, string)                        {}
func (f *fakeSocket) on(link.WebSocketEvent, func(any)) func() { return func() {} }

func TestTheLinksEndpointCutsOffAClientThatStoppedReadingPast4MiBUnsentBeyondTheFrameBeingSent(t *testing.T) {
	fake := &fakeSocket{}
	limited := limitedSocket(fake)
	_ = limited.Send(make([]byte, 10))
	fake.mu.Lock()
	fake.buffered = 5 * 1024 * 1024
	fake.mu.Unlock()
	_ = limited.Send(make([]byte, 10))
	if !reflect.DeepEqual(fake.sent, []int{10}) || fake.terminated != 1 {
		t.Fatalf("sent %v, terminated %d", fake.sent, fake.terminated)
	}
}

func TestPluginSocketsHandTheSocketAndItsRequestToTheHandlerInAScopeWithItsPrincipalNoPrincipalIsRefused(t *testing.T) {
	server := serving(t, []plugin{echo()}, servedOptions{})
	mustRefused(t, server.port, "/echo/lobby", nil, 401)
	mustRefused(t, server.port, "/nope", owner, 404)
	socket := mustSocketTo(t, server.port, "/echo/lobby", owner)
	if got := nextMessage(t, socket); got != "lobby as owner" {
		t.Fatalf("first message = %q", got)
	}
	socket.Send([]byte("hi"), false, nil)
	if got := nextMessage(t, socket); got != "hi" {
		t.Fatalf("echo = %q", got)
	}
	socket.Close(0, "")
}

func TestPluginSocketsAnOversizedFrameClosesOnlyThatSocket1009NeverTheServer(t *testing.T) {
	server := serving(t, []plugin{echo()}, servedOptions{})
	big := mustSocketTo(t, server.port, "/echo/a", owner)
	other := mustSocketTo(t, server.port, "/echo/b", owner)
	nextMessage(t, big)
	nextMessage(t, other)
	big.Send([]byte(strings.Repeat("x", 100)), false, nil)
	if event := receive(t, big.closed, "the close"); event.Code != 1009 {
		t.Fatalf("close code = %d, want 1009", event.Code)
	}
	other.Send([]byte("still here"), false, nil)
	if got := nextMessage(t, other); got != "still here" {
		t.Fatalf("other = %q", got)
	}
	other.Close(0, "")
}

func TestPluginSocketsAThrowingHandlerIsReportedSocketFailedAndClosesItsSocket1011(t *testing.T) {
	server := serving(t, []plugin{{"@app/bad", contributing(HttpContribution{Sockets: []HttpSocketEntry{{
		Pattern: "/bad", Handler: &HttpSocket{Handle: func(*WebSocketHandle, HttpRequest, host.Ctx) error { return errors.New("handler") }},
	}}})}}, servedOptions{})
	socket := mustSocketTo(t, server.port, "/bad", owner)
	if event := receive(t, socket.closed, "the close"); event.Code != 1011 {
		t.Fatalf("close code = %d, want 1011", event.Code)
	}
	errs := server.errors.all()
	if len(errs) != 1 || errs[0].Kind != ErrSocketFailed || errs[0].Pattern != "/bad" || errs[0].Plugin != "@app/bad" {
		t.Fatalf("errors = %+v", errs)
	}
}

// tunnel is a plugin with a host-lifetime object contributing a writable contribution to `Http` that its method `name` sets
// from a list of strings (Pi's `app.tunnel`).
type tunnel struct {
	facet    host.Facet
	contract *contracts.Contract
}

func newTunnel(name string, initial []string, build func(args []string) HttpContribution) tunnel {
	contract := must(contracts.Local(name, contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("name", contracts.NewMethod(contracts.Arr(contracts.Str()))),
	}}))
	facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(contract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			contribution := self.Writable(build(initial))
			self.(host.StoreSelf).Contribute(registries.Registry[any, any](Http), contribution)
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"name": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					list, _ := args.([]any)
					names := make([]string, len(list))
					for i, each := range list {
						names[i], _ = each.(string)
					}
					return nil, set(contribution, build(names))
				},
			}}}, nil
		}, host.ServeOptions{})
	})
	return tunnel{facet: facet, contract: contract}
}

func set(writable state.Writable[any], value HttpContribution) error { return writable.Set(value) }

// name calls the tunnel's `name` with args.
func (tn tunnel) name(t *testing.T, server *testServer, args ...string) {
	t.Helper()
	reference, err := server.host.Get(background, tn.contract, delta.JsonObjectOf(), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Release()
	list := make([]any, len(args))
	for i, each := range args {
		list[i] = each
	}
	if _, err := reference.Call("name", list, background); err != nil {
		t.Fatal(err)
	}
}

// hostsTunnel is a tunnel whose argument is the hosts it allows, and its own sockets.
func hostsTunnel(name string, sockets []HttpSocketEntry) tunnel {
	return newTunnel(name, nil, func(hosts []string) HttpContribution { return HttpContribution{Hosts: hosts, Sockets: sockets} })
}

// Q6.
func TestRevocationKeepsAWritableContributionsOwnSocketsWhenOnlyItsHostsChange(t *testing.T) {
	handler := noopSocket()
	tn := hostsTunnel("app.tunnel", []HttpSocketEntry{{Pattern: "/own", Handler: handler}})
	server := serving(t, []plugin{{"@app/tunnel", tn.facet}}, servedOptions{})
	socket := mustSocketTo(t, server.port, "/own", owner)
	tn.name(t, server, "t.example.com")
	time.Sleep(50 * time.Millisecond)
	if socket.ReadyState() != WebSocketOpenState {
		t.Fatalf("the socket is %d, want open", socket.ReadyState())
	}
	socket.Close(0, "")
}

func TestRevocationTerminatesAPluginsSocketsAtOnceWhenAReloadRemovesItsContributionSealingTheirScopesOthersStayQ6(t *testing.T) {
	scope := make(chan host.Ctx, 1)
	removed := contributing(HttpContribution{Sockets: []HttpSocketEntry{{Pattern: "/removed", Handler: &HttpSocket{Handle: func(_ *WebSocketHandle, _ HttpRequest, ctx host.Ctx) error {
		scope <- ctx
		return nil
	}}}}})
	other := contributing(HttpContribution{Sockets: []HttpSocketEntry{{Pattern: "/other", Handler: noopSocket()}}})
	server := serving(t, []plugin{{"@app/removed", removed}, {"@app/other", other}}, servedOptions{})
	doomed := mustSocketTo(t, server.port, "/removed", owner)
	kept := mustSocketTo(t, server.port, "/other", owner)
	ctx := receive(t, scope, "the handler")
	server.reload(t, plugin{"@app/other", other})
	receive(t, ctx.Done(), "the scope's seal")
	if event := receive(t, doomed.closed, "the close"); event.Code != 1006 {
		t.Fatalf("close code = %d, want 1006", event.Code)
	}
	if kept.ReadyState() != WebSocketOpenState {
		t.Fatalf("the other socket is %d", kept.ReadyState())
	}
	kept.Close(0, "")
}

func TestRevocationTerminatesLinksAndSocketsThroughAHostNoLongerContributedLoopbackOnesStay(t *testing.T) {
	hosts := plugin{"@app/hosts", contributing(HttpContribution{Hosts: []string{"a.example.com"}})}
	server := serving(t, []plugin{echo(), hosts}, servedOptions{links: &LinksOptions{Path: "/chord"}})
	through := withHeaders(owner, map[string]string{"host": "a.example.com"})
	linkSocket := mustSocketTo(t, server.port, "/chord", through)
	socket := mustSocketTo(t, server.port, "/echo/a", through)
	loopback := mustSocketTo(t, server.port, "/echo/b", owner)
	server.reload(t, echo())
	for _, each := range []*clientSocket{linkSocket, socket} {
		if event := receive(t, each.closed, "the close"); event.Code != 1006 {
			t.Fatalf("close code = %d, want 1006", event.Code)
		}
	}
	if loopback.ReadyState() != WebSocketOpenState {
		t.Fatalf("the loopback socket is %d", loopback.ReadyState())
	}
	if got := mustSend(t, server.port, "GET", "/", sendOptions{headers: map[string]string{"host": "a.example.com"}}).status; got != 403 {
		t.Fatalf("the removed host answers %d, want 403", got)
	}
	loopback.Close(0, "")
}

// rawSocket is a raw TCP client upgraded to the plugin socket path through host as the owner.
func rawSocket(t *testing.T, port int, hostName, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+hostName+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: AAAAAAAAAAAAAAAAAAAAAA==\r\nSec-WebSocket-Version: 13\r\nx-principal: owner\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(within))
	answer := make([]byte, 4096)
	n, err := conn.Read(answer)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if !strings.Contains(string(answer[:n]), "101 Switching Protocols") {
		t.Fatalf("answer = %q", answer[:n])
	}
	return conn
}

// endedByServer waits until the server ended conn's connection.
func endedByServer(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(within))
	if _, err := io.Copy(io.Discard, conn); err != nil && !errors.Is(err, io.EOF) {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("the server never ended the connection")
		}
	}
}

// frame is a masked client text frame.
func frame(text string) []byte {
	payload := []byte(text)
	mask := []byte{1, 2, 3, 4}
	out := append([]byte{0x81, byte(0x80 | len(payload))}, mask...)
	for i, b := range payload {
		out = append(out, b^mask[i%4])
	}
	return out
}

// Astra's security audit A1: a policy close left the peer able to invoke the plugin's handlers.
func TestRevocationARawPeerWhoseHostOrContributionLeftCanSendNothingMoreToTheHandler(t *testing.T) {
	var calls collector[string]
	sockets := plugin{"@app/sockets", contributing(HttpContribution{Sockets: []HttpSocketEntry{{
		Pattern: "/echo", Handler: &HttpSocket{Handle: func(socket *WebSocketHandle, _ HttpRequest, _ host.Ctx) error {
			socket.OnMessage(func(message Message) { calls.add(message.Text()) })
			return nil
		}},
	}}})}
	tunnel := plugin{"@app/tunnel", contributing(HttpContribution{Hosts: []string{"t.example"}})}
	server := serving(t, []plugin{sockets, tunnel}, servedOptions{})
	throughHost := rawSocket(t, server.port, "t.example", "/echo")
	defer func() { _ = throughHost.Close() }()
	local := rawSocket(t, server.port, "localhost:"+strconv.Itoa(server.port), "/echo")
	defer func() { _ = local.Close() }()
	server.reload(t, sockets)
	// The peer is cut when the server ends its connection (a router change reaches connections on a task of its own); it
	// writes after that.
	endedByServer(t, throughHost)
	_, _ = throughHost.Write(frame("after the host left"))
	time.Sleep(50 * time.Millisecond)
	server.reload(t)
	endedByServer(t, local)
	_, _ = local.Write(frame("after the contribution left"))
	time.Sleep(50 * time.Millisecond)
	if got := calls.all(); len(got) != 0 {
		t.Fatalf("the handler got %v", got)
	}
}

// Astra's security audit A2: an upgrade authenticating across the removal of its host was still handed out.
func TestRevocationChecksAnUpgradeAgainAfterAuthenticatingOneWhoseHostLeftMeanwhileIsRefused(t *testing.T) {
	gate := newGate()
	started := newGate()
	sockets := plugin{"@app/sockets", contributing(HttpContribution{Sockets: []HttpSocketEntry{{Pattern: "/echo", Handler: noopSocket()}}})}
	server := serving(t, []plugin{sockets, {"@app/tunnel", contributing(HttpContribution{Hosts: []string{"t.example"}})}}, servedOptions{
		authenticate: func(_ HttpRequest, ctx context.Context) (data.Principal, error) {
			started.resolve()
			if err := gate.wait(ctx); err != nil {
				return nil, err
			}
			return principalOwner(), nil
		},
	})
	opening := make(chan error, 1)
	go func() {
		_, err := socketTo(server.port, "/echo", map[string]string{"host": "t.example"}, 0)
		opening <- err
	}()
	receive(t, started.done(), "the authenticator")
	server.reload(t, sockets)
	gate.resolve()
	if err := receive(t, opening, "the upgrade"); err == nil || err.Error() != "refused 403" {
		t.Fatalf("upgrade = %v, want refused 403", err)
	}
}

// Astra's recheck R1, Sol's N5: a client gone while its upgrade authenticated left that work running. Fable's second recheck:
// only a reset (RST) was seen; a clean disconnect (FIN, a tab closing) was not.
func TestCancellationAbortsAnUpgradesAuthenticationWhenItsClient(t *testing.T) {
	for _, how := range []string{"ends its connection", "resets its connection"} {
		t.Run(how, func(t *testing.T) {
			entered := make(chan context.Context, 1)
			server := serving(t, []plugin{{"@app/s", contributing(HttpContribution{Sockets: []HttpSocketEntry{{Pattern: "/s", Handler: noopSocket()}}})}}, servedOptions{
				authenticate: func(_ HttpRequest, ctx context.Context) (data.Principal, error) {
					entered <- ctx
					<-ctx.Done()
					return nil, nil
				},
			})
			conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(server.port))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			_, err = io.WriteString(conn, "GET /s HTTP/1.1\r\nHost: 127.0.0.1:"+strconv.Itoa(server.port)+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
			if err != nil {
				t.Fatal(err)
			}
			ctx := receive(t, entered, "the authenticator")
			if how == "ends its connection" {
				_ = conn.(*net.TCPConn).CloseWrite()
			} else {
				_ = conn.(*net.TCPConn).SetLinger(0)
				_ = conn.Close()
			}
			receive(t, ctx.Done(), "the abort")
		})
	}
}

func TestHeartbeatPingsAPluginSocketAfter15sOfSilenceAndTerminatesIt15sLaterWithoutAnAnswer(t *testing.T) {
	clock := newFakeClock()
	server := serving(t, []plugin{{"@app/quiet", contributing(HttpContribution{Sockets: []HttpSocketEntry{{
		Pattern: "/quiet", Handler: &HttpSocket{Handle: func(socket *WebSocketHandle, _ HttpRequest, _ host.Ctx) error {
			socket.OnMessage(func(message Message) { socket.Send(message.Data, false, nil) })
			return nil
		}},
	}}})}}, servedOptions{after: clock.afterFunc})
	// The silent client never answers a ping (`autoPong: false`).
	silent, err := dialSocket(server.port, "/quiet", owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	silent.conn.SetPingHandler(func(payload string) error {
		silent.emit(webSocketPing, []byte(payload))
		return nil
	})
	answering, err := dialSocket(server.port, "/quiet", owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	pings := make(chan string, 2)
	silent.OncePing(func([]byte) { pings <- "silent" })
	answering.OncePing(func([]byte) { pings <- "answering" })
	echoed := make(chan string, 1)
	answering.OnMessage(func(message Message) { echoed <- message.Text() })
	silent.Start()
	answering.Start()
	// Both upgrades are done (their heartbeat timers are set) once the sockets are open on the server's side too.
	eventually(t, "both heartbeat timers", func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return len(clock.timers) == 2
	})
	clock.advance(15 * time.Second)
	receive(t, pings, "the first ping")
	receive(t, pings, "the second ping")
	// The answering client's pong travels for real (only timers are fake): an echo after it shows it arrived.
	answering.Send([]byte("after the pong"), false, nil)
	receive(t, echoed, "the echo")
	closed := silent.closed
	clock.advance(15 * time.Second)
	if event := receive(t, closed, "the silent client's close"); event.Code != 1006 {
		t.Fatalf("close code = %d, want 1006", event.Code)
	}
	if answering.ReadyState() != WebSocketOpenState {
		t.Fatalf("the answering socket is %d", answering.ReadyState())
	}
	answering.Close(0, "")
}

// Sol's build review: close waited on a peer that never acknowledged its close frame.
func TestCloseTerminatesASocketThatNeverAcknowledgesItsCloseAtCloseTimeout(t *testing.T) {
	server := serving(t, []plugin{{"@app/s", contributing(HttpContribution{Sockets: []HttpSocketEntry{{Pattern: "/s", Handler: noopSocket()}}})}}, servedOptions{closeTimeout: 20 * time.Millisecond})
	raw := rawSocket(t, server.port, "127.0.0.1:"+strconv.Itoa(server.port), "/s")
	defer func() { _ = raw.Close() }()
	started := time.Now()
	server.http.Close(background)
	if took := time.Since(started); took >= 500*time.Millisecond {
		t.Fatalf("close took %v", took)
	}
}

// TestGCKeepsNoUpgradeOnceItsLinkSocketClosed ports "keeps no ws server once its socket closed": Pi spies on the `ws` server each
// upgrade makes (one per frame size) and expects them all collectable once their sockets closed. Here each upgrade makes a
// gorilla Upgrader, a WebSocket and an upgraded record; weak pointers to the last two must clear after the links close.
func TestGCKeepsNoUpgradeOnceItsLinkSocketClosed(t *testing.T) {
	var mu sync.Mutex
	maxPayload := 1024
	server := serving(t, nil, servedOptions{links: &LinksOptions{Path: "/chord", OptionsFunc: func(HttpRequest, data.Principal) (LinkOptions, error) {
		mu.Lock()
		defer mu.Unlock()
		maxPayload++
		return LinkOptions{MaxPayload: maxPayload}, nil
	}}})
	var refs []weak.Pointer[upgraded]
	var sockets []weak.Pointer[WebSocket]
	for range 8 {
		socket := mustSocketTo(t, server.port, "/chord", owner)
		var open []*upgraded
		eventually(t, "the upgrade is kept", func() bool {
			open = server.http.upgrades.snapshot()
			return len(open) == 1
		})
		refs = append(refs, weak.Make(open[0]))
		sockets = append(sockets, weak.Make(open[0].socket))
		open = nil
		socket.Close(0, "")
		receive(t, socket.closed, "the close")
		eventually(t, "the upgrade is dropped", func() bool { return len(server.http.upgrades.snapshot()) == 0 })
	}
	// The closed connections wind down on goroutines of their own: collect for a bounded number of cycles, yielding between.
	const cycles = 2000
	alive := func() int {
		n := 0
		for _, ref := range refs {
			if ref.Value() != nil {
				n++
			}
		}
		for _, ref := range sockets {
			if ref.Value() != nil {
				n++
			}
		}
		return n
	}
	for range cycles {
		runtime.GC()
		if alive() == 0 {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("%d of %d upgrade records and sockets are still reachable after %d GC cycles: a closed link socket is kept", alive(), len(refs)+len(sockets), cycles)
}

// weakOf is a weak pointer to whatever pointer v holds, for objects seen only through an interface.
func weakOf(v any) weak.Pointer[byte] {
	return weak.Make((*byte)(reflect.ValueOf(v).UnsafePointer()))
}

func TestGCKeepsNothingOfAClosedPluginSocket(t *testing.T) {
	var mu sync.Mutex
	var refs []weak.Pointer[byte]
	server := serving(t, []plugin{{"@app/kept", contributing(HttpContribution{Sockets: []HttpSocketEntry{{
		Pattern: "/kept", Handler: &HttpSocket{Handle: func(socket *WebSocketHandle, request HttpRequest, ctx host.Ctx) error {
			mu.Lock()
			refs = append(refs, weakOf(socket), weakOf(request), weakOf(ctx))
			mu.Unlock()
			return nil
		}},
	}}})}}, servedOptions{})
	func() {
		socket := mustSocketTo(t, server.port, "/kept", owner)
		socket.Close(0, "")
		receive(t, socket.closed, "the close")
	}()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(refs) != 3 {
		t.Fatalf("recorded %d objects", len(refs))
	}
	for range 100 {
		runtime.GC()
		alive := 0
		for _, ref := range refs {
			if ref.Value() != nil {
				alive++
			}
		}
		if alive == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the socket, its request or its scope is still kept")
}

func TestAPublicSocketIsAcceptedWithoutAPrincipalAndRunsAnonymousAnotherIsRefused401(t *testing.T) {
	seen := make(chan string, 1)
	handler := &HttpSocket{Handle: func(_ *WebSocketHandle, _ HttpRequest, ctx host.Ctx) error {
		seen <- httpKindOf(ctx.Principal())
		return nil
	}}
	server := serving(t, []plugin{{"@app/sockets", contributing(HttpContribution{Sockets: []HttpSocketEntry{
		{Pattern: "/open", Handler: handler, Public: true},
		{Pattern: "/closed", Handler: noopSocket()},
	}})}}, servedOptions{})
	mustRefused(t, server.port, "/closed", nil, 401)
	socket := mustSocketTo(t, server.port, "/open", nil)
	socket.Start()
	select {
	case kind := <-seen:
		if kind != "anonymous" {
			t.Fatalf("kind = %q", kind)
		}
	case <-time.After(within):
		t.Fatal("the public socket's handler did not run")
	}
	server.close()
}

// Ports packages/chord/src/node/http/http.ts `links.opened` (Pi main c23b9ec1): the hook runs once the link is accepted, with the
// request and the principal; what it returns runs when the socket closes; a hook that fails is reported `hook_failed` and the link
// stays.
func TestTheLinksEndpointCallsOpenedOnceTheLinkIsAcceptedAndItsResultWhenTheSocketCloses(t *testing.T) {
	opened := make(chan [2]string, 1)
	closed := make(chan struct{}, 1)
	server := serving(t, nil, servedOptions{links: &LinksOptions{Path: "/chord", Opened: func(request HttpRequest, principal data.Principal) (func(), error) {
		opened <- [2]string{request.Path(), kindOf(principal)}
		return func() { closed <- struct{}{} }, nil
	}}})
	socket := mustSocketTo(t, server.port, "/chord", owner)
	if got := <-opened; got != [2]string{"/chord", "owner"} {
		t.Fatalf("opened = %v", got)
	}
	select {
	case <-closed:
		t.Fatal("closed ran before the socket closed")
	default:
	}
	socket.Close(1000, "")
	<-closed
}

func TestTheLinksEndpointReportsAFailingOpenedAsHookFailedAndKeepsTheLink(t *testing.T) {
	server := serving(t, nil, servedOptions{links: &LinksOptions{Path: "/chord", Opened: func(HttpRequest, data.Principal) (func(), error) {
		return nil, errors.New("opened")
	}}})
	socket := mustSocketTo(t, server.port, "/chord", owner)
	socket.Close(1000, "")
	// The hook runs after the handshake, on the upgrade's own goroutine: its failure is reported a moment later.
	eventually(t, "the failed hook reported", func() bool { return len(server.errors.all()) > 0 })
	errs := server.errors.all()
	if len(errs) != 1 || errs[0].Kind != ErrHookFailed || errs[0].Hook != HookLinksOpened || errs[0].Error == nil || errs[0].Error.Error() != "opened" {
		t.Fatalf("errors = %+v", errs)
	}
}
