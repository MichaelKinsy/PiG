// Package p2test is the fixture of p2's server tests, the Go twin of test/p2/helpers.ts: a real server under the fake
// provider on a temporary data directory, browser-like client hosts over a real WebSocket, memory-linked clients of any
// principal, bare test hosts, and the waiting helpers every test uses.
package p2test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Main is a test package's TestMain: the test binary is also the session worker (`p2 worker`: the server forks its own
// executable, D473), so a run started with the worker argument is the worker and does not run tests. A worker writes its
// goroutines' stacks to stderr when it gets SIGUSR1 (a failed test's report asks for them).
func Main(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == hosts.WorkerArg {
		dumpStacksOnSignal()
		hosts.RunWorker()
		return
	}
	// The programs plugins run as the test binary (the terminal host, `p2 terminals-host`).
	hosts.RunSubcommand(os.Args)
	os.Exit(m.Run())
}

// Background is the context of test calls made outside any Chord call (a test's own steps).
var Background = chordctx.Background()

// mistakes are Chord's reports of mistakes that would wait (a request nobody serves, a registry without provider): always
// a bug.
var mistakes = []string{"registry_undeclared", "unbound_long", "transaction_cancelled"}

// reportTo is a test host's `OnError`: mistakes are printed (the full run greps for them); record gets every error.
func reportTo(record func(host.HostError)) func(host.HostError) {
	return func(failure host.HostError) {
		text := fmt.Sprint(failure)
		for _, mistake := range mistakes {
			if strings.Contains(text, mistake) {
				fmt.Fprintf(os.Stderr, "p2 test host: %s\n", text)
			}
		}
		if record != nil {
			record(failure)
		}
	}
}

// closedSockets are the connections whose reader ended (the peer closed them, or they were closed here).
var closedSockets sync.Map

// SocketClosed is whether conn (a Fixture.Client's) is closed: Pi's `socket.readyState === 3`.
func SocketClosed(conn *websocket.Conn) bool {
	_, closed := closedSockets.Load(conn)
	return closed
}

// socket is a gorilla WebSocket as link.WebSocketLike: binary frames, one message per frame.
type socket struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	listeners map[link.WebSocketEvent][]func(any)
	writeMu   sync.Mutex
	closed    bool
}

//portlint:allow golifetime read returns when the socket closes
func (s *socket) start() { go s.read() }

func (s *socket) read() {
	for {
		kind, payload, err := s.conn.ReadMessage()
		if err != nil {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			closedSockets.Store(s.conn, struct{}{})
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

func (s *socket) emit(event link.WebSocketEvent, value any) {
	s.mu.Lock()
	listeners := slices.Clone(s.listeners[event])
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(value)
	}
}

func (s *socket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 3
	}
	return 1
}

func (s *socket) SetBinaryType(string) {}

func (s *socket) Send(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (s *socket) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.conn.Close()
}

func (s *socket) AddEventListener(event link.WebSocketEvent, listener func(data any)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners[event] = append(s.listeners[event], listener)
	return func() {}
}

// Dial opens a WebSocket to a p2 server's `/chord` on port, sending headers (a tunnel Host, a guest cookie, a relay).
func Dial(port int, headers map[string]string) (*websocket.Conn, error) {
	requestHeaders := http.Header{}
	for name, value := range headers {
		requestHeaders.Set(name, value)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, response, err := dialer.Dial("ws://127.0.0.1:"+strconv.Itoa(port)+"/chord", requestHeaders)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return conn, err
}

// ClientHost is a browser-like client host connected to a p2 server's `/chord` over conn, as the web UI connects. Its
// holds wait until the host has loaded (facets: web facets, say). Close it with Host.Close.
func ClientHost(conn *websocket.Conn, facets ...host.HostFacet) (host.Host, error) {
	client, err := p2runtime.NewHost(host.HostOptions{OnError: reportTo(nil)})
	if err != nil {
		return nil, err
	}
	s := &socket{conn: conn, listeners: map[link.WebSocketEvent][]func(any){}}
	if err := client.Connect(link.WebSocketLink(s), host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, Background); err != nil {
		return nil, err
	}
	s.start()
	if _, err := client.Load(facets, Background); err != nil {
		return nil, err
	}
	return client, nil
}

// Fixture is a running server on a temporary data directory, with its clients.
type Fixture struct {
	Server    *hosts.P2Server
	DataDir   string
	Workspace string

	t        testing.TB
	root     string
	mu       sync.Mutex
	browsers []host.Host
	sessions []string
}

// Rebuilds stands in for rebuilt plugin sources: pass Revision as `ServerOptions.BuildRevision`, and Rebuild(plugin)
// before a Reload that must roll the plugin out (Pi's test writes a changed source; a reload of unchanged code is a no-op).
type Rebuilds struct {
	mu    sync.Mutex
	count map[string]int
}

// Rebuild changes the plugin's build revision.
func (r *Rebuilds) Rebuild(plugin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == nil {
		r.count = map[string]int{}
	}
	r.count[plugin]++
}

// Revision is the plugin's build revision: empty until its first Rebuild, which is the build the server starts on.
func (r *Rebuilds) Revision(plugin string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count[plugin] == 0 {
		return ""
	}
	return strconv.Itoa(r.count[plugin])
}

// StartFixture starts a server under the fake provider (`startFixture`): `options` override the defaults (a free port, no
// extra environment); the test binary is the worker. The fixture closes with the test.
func StartFixture(t testing.TB, options hosts.ServerOptions) *Fixture {
	t.Helper()
	root, err := os.MkdirTemp("", "p2-test-")
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if options.DataDir == "" {
		options.DataDir = dataDir
	}
	if options.Port == nil {
		zero := 0
		options.Port = &zero
	}
	if options.Fake == nil {
		fake := true
		options.Fake = &fake
	}
	if options.Entry == "" {
		entry, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		options.Entry, options.ExecArgv = entry, []string{hosts.WorkerArg}
	}
	server, err := hosts.StartServer(options)
	if err != nil {
		_ = os.RemoveAll(root)
		t.Fatal(err)
	}
	fixture := &Fixture{Server: server, DataDir: options.DataDir, Workspace: workspace, t: t, root: root}
	fixture.trackSessions()
	t.Cleanup(fixture.Close)
	// Cleanups run last-in first-out: the failure report is made before the workers go.
	t.Cleanup(fixture.reportWorkers)
	return fixture
}

// Browser is a browser-like client host over a real WebSocket (as the web UI connects), with facets loaded (web facets).
func (f *Fixture) Browser(facets ...host.HostFacet) host.Host {
	client, _ := f.Client(nil, facets...)
	return client
}

// Client is a client host whose WebSocket sends headers (a tunnel Host, a guest cookie), and that socket.
func (f *Fixture) Client(headers map[string]string, facets ...host.HostFacet) (host.Host, *websocket.Conn) {
	f.t.Helper()
	conn, err := Dial(f.Server.Port, headers)
	if err != nil {
		f.t.Fatalf("dial /chord: %v", err)
	}
	client, err := ClientHost(conn, facets...)
	if err != nil {
		f.t.Fatalf("client host: %v", err)
	}
	f.mu.Lock()
	f.browsers = append(f.browsers, client)
	f.mu.Unlock()
	return client, conn
}

// As is a memory-linked client the server accepts as principal (a relay owner, a worker, a guest of a test).
func (f *Fixture) As(principal p2runtime.Principal) host.Host {
	f.t.Helper()
	down, up := link.MemoryLinkPair()
	if err := f.Server.Host.Accept(down, host.LinkOptions{Principal: p2runtime.ToChord(principal)}, Background); err != nil {
		f.t.Fatal(err)
	}
	client, err := p2runtime.NewHost(host.HostOptions{OnError: reportTo(nil)})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := client.Connect(up, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, Background); err != nil {
		f.t.Fatal(err)
	}
	if _, err := client.Load(nil, Background); err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	f.browsers = append(f.browsers, client)
	f.mu.Unlock()
	return client
}

// Close closes the clients, the server and the temporary directory (once).
func (f *Fixture) Close() {
	f.mu.Lock()
	browsers := f.browsers
	f.browsers = nil
	f.mu.Unlock()
	for _, browser := range browsers {
		_ = browser.Close(Background)
	}
	if f.Server != nil {
		_ = f.Server.Close(Background)
		f.Server = nil
		_ = os.RemoveAll(f.root)
	}
}

// Narrowed is a live reference narrowed to a variant.
type Narrowed struct {
	*host.Reference
	Variant string
}

// HoldIn holds contract(params) on h, live, and narrowed to variant: it fails with `invalid/member` when the host's
// principal gets another variant, and with the error when the reference ends or fails broken.
func HoldIn(h host.Host, contract *contracts.Contract, params *delta.JsonObject, variant string) (*host.Reference, error) {
	ref, err := h.Hold(Background, contract, params, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(Background, HoldLiveTimeout)
	defer cancel()
	if err := p2runtime.WhenLive(ctx, ref); err != nil {
		state := ref.State().Value()
		ref.Release()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("the reference to %s was not live within %v (state %v, %v): a worker that failed to start leaves its address failed until a retry", contract.Name, HoldLiveTimeout, state.Kind, p2runtime.ErrorOf(ref))
		}
		return nil, err
	}
	return narrowed(ref, variant)
}

// HoldLiveTimeout bounds how long HoldIn waits for a reference to go live: a worker's start takes 60 s at most, and one that
// failed to start leaves its address failed (a transient failure the reference keeps waiting through), where a wait with no
// bound holds the test until the test binary's own timeout.
var HoldLiveTimeout = 90 * time.Second

// BoundIn is HoldIn for an older contract version: it fails, naming the state, when the reference does not bind (a version
// the provider does not accept is left `failed { waiting }` with `contract_mismatch`, where HoldIn would wait).
func BoundIn(h host.Host, contract *contracts.Contract, params *delta.JsonObject, variant string) (*host.Reference, error) {
	ref, err := h.Hold(Background, contract, params, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	state, err := UntilNative(ref.State(), func(state *host.RefState) bool {
		return state.Kind == host.RefStateLive || state.Kind == host.RefStateFailed
	}, 10*time.Second)
	if err != nil {
		ref.Release()
		return nil, err
	}
	if state.Kind != host.RefStateLive {
		ref.Release()
		return nil, fmt.Errorf("not bound: %v", state.Kind)
	}
	return narrowed(ref, variant)
}

func narrowed(ref *host.Reference, variant string) (*host.Reference, error) {
	got, _, err := ref.Variant()
	if err != nil || got != variant {
		ref.Release()
		return nil, data.Invalid(data.ReasonMember, fmt.Sprintf("expected variant %s, got %s", variant, got))
	}
	return ref, nil
}

// FailureOf is the error of ref's first failure or end, whatever its cause: a type nobody serves leaves a hold `failed
// { waiting }` (it would bind once served), which WhenLive keeps waiting through.
func FailureOf(ref *host.Reference) (error, error) {
	state, err := UntilNative(ref.State(), func(state *host.RefState) bool {
		return state.Kind == host.RefStateFailed || state.Kind == host.RefStateEnded
	}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if state.Kind == host.RefStateEnded && state.Error != nil {
		return state.Error, nil
	}
	if state.Kind == host.RefStateFailed && state.Cause.Error != nil {
		return state.Cause.Error, nil
	}
	return errors.New("failed without an error"), nil
}

// JSONSource is a state member, a derive or a registry value as the helpers follow it.
type JSONSource interface {
	Value() (delta.JsonValue, error)
	Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
}

// Until is the first value of source that satisfies predicate (following it meanwhile), or an error after timeout
// (20 s by default when zero).
func Until(source JSONSource, predicate func(delta.JsonValue) bool, timeout time.Duration) (delta.JsonValue, error) {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(Background, timeout)
	defer cancel()
	watch, err := source.Watch(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = watch.Stop() }()
	changed := make(chan delta.JsonValue, 1)
	last := watch.Value()
	if predicate(last) {
		return last, nil
	}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			select {
			case changed <- call.Value:
			default:
				// The newest wins.
				select {
				case <-changed:
				default:
				}
				changed <- call.Value
			}
		}
		return nil
	})
	for {
		select {
		case value := <-changed:
			last = value
			if predicate(value) {
				return value, nil
			}
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out after %s; last value: %.800s", timeout, describe(last))
		}
	}
}

// describe is a value as JSON for a failure message (a delta value prints as pointers otherwise).
func describe(value delta.JsonValue) string {
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// UntilNative is Until for a native watchable (a reference's state, a registry's value).
func UntilNative[T any](source chord.NativeWatchable[T], predicate func(T) bool, timeout time.Duration) (T, error) {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	found := make(chan T, 1)
	stop := p2runtime.Follow(source, func(value T) {
		if predicate(value) {
			select {
			case found <- value:
			default:
			}
		}
	})
	defer stop()
	select {
	case value := <-found:
		return value, nil
	case <-time.After(timeout):
		var zero T
		return zero, fmt.Errorf("timed out after %s", timeout)
	}
}

// Settled is a defined value of source after a quiet publication window (25 ms by default), not after future or background
// work finishes. Await the producing call or reference first; use Until for an expected revision or an asynchronous
// transition.
func Settled(source JSONSource, quiet time.Duration, timeout time.Duration) (delta.JsonValue, error) {
	if quiet == 0 {
		quiet = 25 * time.Millisecond
	}
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(Background, timeout)
	defer cancel()
	watch, err := source.Watch(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = watch.Stop() }()
	changed := make(chan struct{}, 1)
	latest := watch.Value()
	var mu sync.Mutex
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			mu.Lock()
			latest = call.Value
			mu.Unlock()
			select {
			case changed <- struct{}{}:
			default:
			}
		}
		return nil
	})
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		select {
		case <-changed:
			timer.Reset(quiet)
		case <-timer.C:
			mu.Lock()
			value := latest
			mu.Unlock()
			if value != nil {
				return value, nil
			}
			timer.Reset(quiet)
		case <-ctx.Done():
			return nil, fmt.Errorf("state did not settle after %s", timeout)
		}
	}
}

// Poll calls check every 25 ms until it holds, or fails after timeout (20 s by default when zero).
func Poll(check func() (bool, error), timeout time.Duration, what string) error {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		ok, err := check()
		if err != nil || ok {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// ExpectChord asserts a stable ChordError code rather than matching an implementation's error wording.
func ExpectChord(t testing.TB, err error, code data.ChordErrorCode) *data.ChordError {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != code {
		t.Fatalf("expected ChordError %s, got %v", code, err)
	}
	return chordErr
}

// HTTPResult is an HTTP response as the tests read it.
type HTTPResult struct {
	Status  int
	Headers http.Header
	Body    string
}

// HTTPOptions are the method, headers and body of HTTP's request.
type HTTPOptions struct {
	Method  string
	Headers map[string]string
	Body    io.Reader
}

// HTTP is a request to the fixture's server (any Host, cookie and body).
func HTTP(port int, path string, options HTTPOptions) (HTTPResult, error) {
	method := options.Method
	if method == "" {
		method = http.MethodGet
	}
	request, err := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(port)+path, options.Body)
	if err != nil {
		return HTTPResult{}, err
	}
	for name, value := range options.Headers {
		if strings.EqualFold(name, "Host") {
			request.Host = value
			continue
		}
		request.Header.Set(name, value)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return HTTPResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return HTTPResult{Status: response.StatusCode, Headers: response.Header, Body: string(body)}, err
}
