package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Test support for the HTTP component (slice 10 §2; Pi's test/next-node/http/support.ts): a server host with the component,
// the app's configuration and authenticator, and plugin facets contributing to `Http`; raw requests (any `Host`, any body) and
// WebSocket clients against its listener.

var background = chordctx.Background()

// within is how long a test waits for something that must happen.
const within = 30 * time.Second

func principalOwner() data.Principal { return delta.JsonObjectOf("kind", "owner") }

func principalServer() data.Principal { return delta.JsonObjectOf("kind", "server") }

func principalGuest(id string) data.Principal {
	return delta.JsonObjectOf("kind", "guest", "id", id)
}

// collector is a list written from several goroutines.
type collector[T any] struct {
	mu    sync.Mutex
	items []T
}

func (c *collector[T]) add(item T) {
	c.mu.Lock()
	c.items = append(c.items, item)
	c.mu.Unlock()
}

func (c *collector[T]) all() []T {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]T(nil), c.items...)
}

func (c *collector[T]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// plugin is a facet by its plugin name.
type plugin struct {
	name  string
	facet host.Facet
}

// Served is a running server: its host, its component, its port once loaded, what both reported.
type testServer struct {
	t          *testing.T
	host       host.Host
	http       HttpComponent
	component  *component
	port       int
	errors     *collector[HttpError]
	hostErrors *collector[host.HostError]
	plugins    []plugin
	core       []host.HostFacet
	listen     bool
	// registryCalls counts host.Registry calls (Pi's `vi.spyOn(host, "registry")`).
	registryCalls *countingHost
}

// countingHost counts the registry reads the component makes.
type countingHost struct {
	host.Host
	mu    sync.Mutex
	calls int
}

func (h *countingHost) Registry(r registries.Registry[any, any], ctx context.Context, options host.GetOptions) (*registries.RegistryValueSource, error) {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	return h.Host.Registry(r, ctx, options)
}

func (h *countingHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// contributing is a plugin facet contributing contribution to `Http`.
func contributing(contribution HttpContribution) host.Facet {
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](Http), contribution)
	})
}

// authenticating is a plugin facet contributing an authenticator.
func authenticating(authenticator Authenticator) host.Facet {
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](Authenticators), authenticator)
	})
}

// servedOptions are the options of serving.
type servedOptions struct {
	// noListen: `listen: undefined`, no listener.
	noListen     bool
	links        *LinksOptions
	closeTimeout time.Duration
	authenticate Authenticator
	// noLoad: the host is made, not loaded.
	noLoad       bool
	readyTimeout time.Duration
	address      *contracts.Address
	after        afterFunc
}

// serving builds the component on a new server host (listening on loopback, a free port), then loads it with the app's
// authenticator (`@app/auth`: the `x-principal` header names one, else anonymous) and plugins unless noLoad.
func serving(t *testing.T, plugins []plugin, options servedOptions) *testServer {
	t.Helper()
	errs := &collector[HttpError]{}
	hostErrs := &collector[host.HostError]{}
	hostOptions := host.HostOptions{Principal: principalServer(), OnError: func(err host.HostError) { hostErrs.add(err) }, Address: options.address}
	if options.readyTimeout != 0 {
		ready := options.readyTimeout
		hostOptions.ReadyTimeout = &ready
	}
	inner, err := host.CreateHost(hostOptions)
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingHost{Host: inner}
	httpOptions := HttpOptions{Links: options.links, CloseTimeout: options.closeTimeout, OnError: errs.add}
	if !options.noListen {
		httpOptions.Listen = &ListenAddress{Host: "127.0.0.1", Port: 0}
	}
	after := options.after
	if after == nil {
		after = realAfter
	}
	component := newComponent(counting, httpOptions, background, after)
	authenticate := options.authenticate
	if authenticate == nil {
		authenticate = func(request HttpRequest, _ context.Context) (data.Principal, error) {
			return principalOf(request.Headers()), nil
		}
	}
	server := &testServer{
		t: t, host: inner, http: component, component: component, errors: errs, hostErrors: hostErrs, plugins: plugins,
		listen: !options.noListen, registryCalls: counting,
		core: []host.HostFacet{
			{Name: "chord.http", Facet: component.Facet()},
			{Name: "@app/auth", Facet: authenticating(authenticate)},
		},
	}
	t.Cleanup(func() { server.closeQuietly() })
	if !options.noLoad {
		server.load()
	}
	return server
}

// facets are the core facets and plugins (or more, by name), as loaded.
func (s *testServer) facets(more ...plugin) []host.HostFacet {
	if more == nil {
		more = s.plugins
	}
	facets := append([]host.HostFacet(nil), s.core...)
	for _, each := range more {
		facets = append(facets, host.HostFacet{Name: each.name, Facet: each.facet})
	}
	return facets
}

// load loads the host with the facets and waits for the listener's port.
func (s *testServer) load() {
	s.t.Helper()
	if err := s.host.Load(s.facets(), background); err != nil {
		s.t.Fatal(err)
	}
	if s.listen {
		address, err := s.http.Listening(background)
		if err != nil {
			s.t.Fatal(err)
		}
		s.port = address.Port
	}
}

// close closes the component, then the host.
func (s *testServer) close() {
	s.t.Helper()
	s.http.Close(background)
	if err := s.host.Close(background); err != nil {
		s.t.Fatal(err)
	}
}

func (s *testServer) closeQuietly() {
	s.http.Close(background)
	_ = s.host.Close(background)
}

// principalOf is the principal an `x-principal` header names: `owner`, `guest:<id>`; none without it.
func principalOf(headers nethttp.Header) data.Principal {
	named := headers.Get("x-principal")
	if named == "owner" {
		return principalOwner()
	}
	if id, ok := strings.CutPrefix(named, "guest:"); ok {
		return principalGuest(id)
	}
	return nil
}

// answer is a response as a test reads it.
type answer struct {
	status  int
	headers nethttp.Header
	body    []byte
}

func (a answer) text() string { return string(a.body) }

// header is a header's first value, lowercased name lookups as Node's `IncomingHttpHeaders`.
func (a answer) header(name string) string { return a.headers.Get(name) }

// sendOptions are what send takes besides the method and the path.
type sendOptions struct {
	headers map[string]string
	body    string
	// keepAlive uses the shared client instead of a connection of its own.
	client *nethttp.Client
}

// send is a request to port with raw control: `Host` defaults to `127.0.0.1:<port>`; body is sent as is.
func send(t testing.TB, port int, method, path string, options sendOptions) (answer, error) {
	t.Helper()
	client := options.client
	if client == nil {
		client = &nethttp.Client{Transport: &nethttp.Transport{DisableKeepAlives: true}, CheckRedirect: func(*nethttp.Request, []*nethttp.Request) error { return nethttp.ErrUseLastResponse }}
	}
	var body io.Reader
	if options.body != "" {
		body = strings.NewReader(options.body)
	}
	request, err := nethttp.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(port)+path, body)
	if err != nil {
		return answer{}, err
	}
	request.Host = "127.0.0.1:" + strconv.Itoa(port)
	for name, value := range options.headers {
		if strings.EqualFold(name, "host") {
			request.Host = value
			continue
		}
		if strings.EqualFold(name, "transfer-encoding") && value == "chunked" {
			request.TransferEncoding = []string{"chunked"}
			request.ContentLength = -1
			continue
		}
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return answer{}, err
	}
	defer response.Body.Close()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		return answer{}, err
	}
	return answer{status: response.StatusCode, headers: response.Header, body: text}, nil
}

// mustSend is send that fails the test on a transport error.
func mustSend(t testing.TB, port int, method, path string, options sendOptions) answer {
	t.Helper()
	got, err := send(t, port, method, path, options)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return got
}

// clientSocket is a WebSocket client from socketTo: its messages queued from its start, its close kept.
type clientSocket struct {
	*WebSocket
	inbox  chan string
	closed chan CloseEvent
}

// dialSocket is a WebSocket client to path on port (`Host` loopback), with headers, not yet reading (a caller attaching
// listeners at once, as a link does, starts it); it returns the error `refused <status>` when the upgrade is refused.
func dialSocket(port int, path string, headers map[string]string, maxPayload int) (*clientSocket, error) {
	header := nethttp.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	dialer := websocket.Dialer{HandshakeTimeout: within}
	conn, response, err := dialer.Dial("ws://127.0.0.1:"+strconv.Itoa(port)+path, header)
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("refused %d", response.StatusCode)
		}
		return nil, err
	}
	if maxPayload > 0 {
		conn.SetReadLimit(int64(maxPayload))
	}
	socket := &clientSocket{WebSocket: newWebSocket(conn), inbox: make(chan string, 1024), closed: make(chan CloseEvent, 1)}
	socket.Once(link.WebSocketClose, func(data any) { socket.closed <- data.(CloseEvent) })
	socket.On(link.WebSocketMessage, func(data any) {
		if text, ok := data.(string); ok {
			socket.inbox <- text
		}
	})
	return socket, nil
}

// socketTo is dialSocket reading from the start: frames sent at once by the server arrive with the upgrade, queued.
func socketTo(port int, path string, headers map[string]string, maxPayload int) (*clientSocket, error) {
	socket, err := dialSocket(port, path, headers, maxPayload)
	if err != nil {
		return nil, err
	}
	socket.Start()
	return socket, nil
}

func mustSocketTo(t testing.TB, port int, path string, headers map[string]string) *clientSocket {
	t.Helper()
	socket, err := socketTo(port, path, headers, 0)
	if err != nil {
		t.Fatalf("socketTo %s: %v", path, err)
	}
	return socket
}

// nextMessage is the next text message of a socket socketTo opened.
func nextMessage(t testing.TB, socket *clientSocket) string {
	t.Helper()
	select {
	case text := <-socket.inbox:
		return text
	case <-time.After(within):
		t.Fatal("no message")
		return ""
	}
}

// closeOf is the close code and reason of socket's close.
func closeOf(socket *WebSocket) <-chan CloseEvent {
	result := make(chan CloseEvent, 1)
	if socket.ReadyState() == WebSocketClosedState {
		result <- CloseEvent{Code: 1006}
		return result
	}
	socket.Once(link.WebSocketClose, func(data any) { result <- data.(CloseEvent) })
	return result
}

// receive is the value from ch, failing the test when none comes.
func receive[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(within):
		t.Fatalf("%s: nothing came", what)
		var zero T
		return zero
	}
}

// eventually waits for condition.
func eventually(t testing.TB, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// gate is a Promise.withResolvers<void>().
type gate struct {
	once sync.Once
	ch   chan struct{}
}

func newGate() *gate { return &gate{ch: make(chan struct{})} }

func (g *gate) resolve() { g.once.Do(func() { close(g.ch) }) }

// wait blocks until resolved or ctx ends.
func (g *gate) wait(ctx context.Context) error {
	select {
	case <-g.ch:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (g *gate) done() <-chan struct{} { return g.ch }

// loopback is a loopback request to Fetch directly, as a mounted server would make it.
func loopback(path string, mutate ...func(*nethttp.Request)) *nethttp.Request {
	request, err := nethttp.NewRequest("GET", "http://127.0.0.1:8788"+path, nil)
	if err != nil {
		panic(err)
	}
	request.Header.Set("Host", "127.0.0.1:8788")
	for _, each := range mutate {
		each(request)
	}
	return request
}

// loopbackConnection is the connection of loopback's requests.
var loopbackConnection = Connection{LocalAddress: "127.0.0.1", LocalPort: 8788}

// fetchText is a response's body as text.
func fetchText(t testing.TB, response *nethttp.Response) string {
	t.Helper()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return string(text)
}

// jsonOf is a route response of a JSON value.
func jsonOf(value any) HttpResponse { return HttpResponse{Kind: KindJSON, Value: value} }

// htmlOf is a route response of html.
func htmlOf(body string) HttpResponse { return HttpResponse{Kind: KindHTML, Body: body} }

// rawExchange is what port answers a raw request text with: its status line and body (Pi's `raw`).
func rawExchange(t testing.TB, port int, text string) (status, body string) {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(within))
	out, _ := io.ReadAll(conn)
	head, rest, _ := bytes.Cut(out, []byte("\r\n\r\n"))
	line, _, _ := strings.Cut(string(head), "\r\n")
	return line, string(rest)
}

// fakeClock is a clock of virtual time for the heartbeat (Pi's fake `setTimeout`).
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers map[*fakeTimer]struct{}
}

type fakeTimer struct {
	clock *fakeClock
	at    time.Duration
	f     func()
	live  bool
}

func newFakeClock() *fakeClock { return &fakeClock{timers: map[*fakeTimer]struct{}{}} }

func (c *fakeClock) afterFunc(d time.Duration, f func()) timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now + d, f: f, live: true}
	c.timers[t] = struct{}{}
	return t
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := t.live
	t.live = false
	delete(t.clock.timers, t)
	return was
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := t.live
	t.live = true
	t.at = t.clock.now + d
	t.clock.timers[t] = struct{}{}
	return was
}

// advance moves time by d, firing every timer due, in order.
func (c *fakeClock) advance(d time.Duration) {
	target := func() time.Duration { c.mu.Lock(); defer c.mu.Unlock(); return c.now + d }()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for t := range c.timers {
			if t.at <= target && (next == nil || t.at < next.at) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		c.now = next.at
		next.live = false
		delete(c.timers, next)
		c.mu.Unlock()
		next.f()
	}
}

var errTest = errors.New("test error")
