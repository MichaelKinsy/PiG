package http

import (
	"bufio"
	"context"
	"errors"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Go-only regression tests of the lane review: each fails on the code before its
// fix. They have no upstream test file.

// delayedHandler is a listener's handler whose answers are written a little late: the window between a request's answer and its
// bytes on the wire that `close` must not cut.
type delayedHandler struct {
	inner Handler
	delay time.Duration
}

func (d delayedHandler) Serve(incoming Incoming, connection Connection) Served {
	served := d.inner.Serve(incoming, connection)
	time.Sleep(d.delay)
	return served
}

func (d delayedHandler) Refusal(status int) Answer { return d.inner.Refusal(status) }

func (d delayedHandler) Upgrade(w nethttp.ResponseWriter, request *nethttp.Request) {
	d.inner.Upgrade(w, request)
}

// Review B1: close sealed a request whose route never answered, and the listener's close that follows cut the 503 before it was
// written (the client saw EOF).
func TestReviewCloseWritesTheAnswerOfASealedRequestBeforeTheListenerCloses(t *testing.T) {
	stuck := newGate()
	t.Cleanup(stuck.resolve)
	entered := newGate()
	server := serving(t, []plugin{{"@app/stuck", contributing(HttpContribution{Routes: []HttpRouteEntry{
		publicRoute("GET /stuck", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			entered.resolve()
			<-stuck.done()
			return HttpResponse{}, nil
		}),
	}})}}, servedOptions{noListen: true, closeTimeout: 50 * time.Millisecond})
	listener, err := ListenNode(delayedHandler{inner: server.http, delay: 150 * time.Millisecond}, ListenAddress{Host: "127.0.0.1"}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(listener.Closed)
	type sent struct {
		answer answer
		err    error
	}
	answered := httpAsync(func() sent {
		got, err := send(t, listener.Address.Port, "GET", "/stuck", sendOptions{})
		return sent{got, err}
	})
	receive(t, entered.done(), "the route")
	server.http.Close(background)
	// The listener's close, as the native's runs it after the component's.
	listener.Closed()
	if got := receive(t, answered, "the answer"); got.err != nil || got.answer.status != 503 {
		t.Fatalf("the sealed request's answer = %d, %v, want 503", got.answer.status, got.err)
	}
}

// Review B2: an upgrade past its last closing check when close took its snapshot was kept after it: no 1001, no termination at
// the timeout.
func TestReviewCloseClosesOrRefusesAnUpgradeThatWasPastTheLastCheck(t *testing.T) {
	entered := newGate()
	release := newGate()
	t.Cleanup(release.resolve)
	server := serving(t, nil, servedOptions{
		closeTimeout: 3 * time.Second,
		links: &LinksOptions{Path: "/chord", OptionsFunc: func(HttpRequest, data.Principal) (LinkOptions, error) {
			// After the checks and the authenticators, before the socket is kept.
			entered.resolve()
			<-release.done()
			return LinkOptions{}, nil
		}},
	})
	type dialed struct {
		socket *clientSocket
		err    error
	}
	opening := httpAsync(func() dialed {
		socket, err := socketTo(server.port, "/chord", owner, 0)
		return dialed{socket, err}
	})
	receive(t, entered.done(), "the links options hook")
	closing := httpAsync(func() struct{} {
		server.http.Close(background)
		return struct{}{}
	})
	eventually(t, "close refuses", server.http.isClosing)
	// Close has taken its snapshot of the open sockets by now.
	time.Sleep(150 * time.Millisecond)
	release.resolve()
	got := receive(t, opening, "the upgrade")
	receive(t, closing, "close")
	if got.err != nil {
		if got.err.Error() != "refused 503" {
			t.Fatalf("the upgrade failed with %v, want refused 503", got.err)
		}
		return
	}
	// Kept before close ran: it was closed 1001 with the others.
	select {
	case event := <-got.socket.closed:
		if event.Code != 1001 && event.Code != 1006 {
			t.Fatalf("close code = %d, want 1001", event.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("an upgrade handed out while close ran outlived it")
	}
}

// Review S2: a redirect target with a CR or LF (or a character above U+00FF) answered 503 unreported; Pi answers 500 and
// reports `response_failed`.
func TestReviewAnInvalidRedirectTargetIsA500ReportedResponseFailed(t *testing.T) {
	for _, target := range []string{"/a\r\nSet-Cookie: x=1", "/\u20ac"} {
		server := answering(t, HttpResponse{Kind: KindRedirect, To: target})
		got := mustSend(t, server.port, "GET", "/r", sendOptions{})
		if got.status != 500 || len(got.headers.Values("Set-Cookie")) != 0 || got.header("x-content-type-options") != "nosniff" {
			t.Fatalf("%q: %d %v", target, got.status, got.headers)
		}
		errs := server.errors.all()
		if len(errs) != 1 || errs[0].Kind != ErrResponseFailed || errs[0].Pattern != "GET /r" {
			t.Fatalf("%q: errors = %+v", target, errs)
		}
		server.close()
	}
}

// Review S2: a contributed CSP source that is no header value reached `mustSet` (a panic recovered as a 503) from the default
// headers; the route's answer is a reported 500 and a refusal keeps the baseline.
func TestReviewAContributedCSPSourceThatIsNoHeaderValueIsAReported500AndRefusalsKeepTheBaseline(t *testing.T) {
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{
		Routes: []HttpRouteEntry{publicRoute("GET /r", func(HttpRequest, host.Ctx) (HttpResponse, error) { return htmlOf("x"), nil })},
		Csp:    []CspEntry{{Directive: "img-src", Sources: []string{"a\r\nSet-Cookie: x=1"}}},
	})}}, servedOptions{})
	got := mustSend(t, server.port, "GET", "/r", sendOptions{})
	if got.status != 500 || len(got.headers.Values("Set-Cookie")) != 0 || !strings.HasPrefix(got.header("content-security-policy"), "default-src 'none'") {
		t.Fatalf("route: %d %v", got.status, got.headers)
	}
	missing := mustSend(t, server.port, "GET", "/nope", sendOptions{})
	if missing.status != 404 || missing.header("x-content-type-options") != "nosniff" || strings.Contains(missing.header("content-security-policy"), "img-src") {
		t.Fatalf("refusal: %d %v", missing.status, missing.headers)
	}
	if errs := server.errors.all(); len(errs) != 1 || errs[0].Kind != ErrResponseFailed {
		t.Fatalf("errors = %+v", errs)
	}
}

// Review S3: `URLSearchParams` keeps a pair with a `;` or a bad escape, and its order; url.ParseQuery drops them.
func TestReviewQueryParsesAsURLSearchParams(t *testing.T) {
	query := ParseURLSearchParams("a=1;b=2&q=100%&x=%E0%A4%A&k&&+p+=a+b&a=2&e=%e2%82%ac&bad=%FF")
	want := []QueryPair{
		{"a", "1;b=2"}, {"q", "100%"}, {"x", "\ufffd%A"}, {"k", ""}, {" p ", "a b"}, {"a", "2"}, {"e", "\u20ac"}, {"bad", "\ufffd"},
	}
	got := query.Pairs()
	if len(got) != len(want) {
		t.Fatalf("pairs = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pair %d = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
	if values := query.GetAll("a"); len(values) != 2 || values[0] != "1;b=2" || values[1] != "2" {
		t.Fatalf("a = %q", values)
	}
	if first, ok := query.Get("q"); !ok || first != "100%" || query.Has("nope") {
		t.Fatalf("q = %q, %v", first, ok)
	}
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{Routes: []HttpRouteEntry{
		publicRoute("GET /q", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
			value, _ := request.Query().Get("a")
			return htmlOf(value), nil
		}),
	}})}}, servedOptions{})
	if got := mustSend(t, server.port, "GET", "/q?a=1;b=2", sendOptions{}).text(); got != "1;b=2" {
		t.Fatalf("a = %q, want 1;b=2", got)
	}
}

// Review S4: Chord's listener gives a GET or HEAD no body (Pi's Web `Request` has none), so `json()` of one is a 400.
func TestReviewAGETBodyIsNoBodyOnTheListenerAndOnMountedRequests(t *testing.T) {
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{Routes: []HttpRouteEntry{
		publicRoute("GET /json", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
			value, err := request.JSON()
			if err != nil {
				return HttpResponse{}, err
			}
			return jsonOf(value), nil
		}),
	}})}}, servedOptions{})
	if got := mustSend(t, server.port, "GET", "/json", sendOptions{body: "{}"}).status; got != 400 {
		t.Fatalf("a GET with a body: %d, want 400", got)
	}
	mounted := httptest.NewRequest("GET", "/json", strings.NewReader("{}"))
	mounted.Host = "127.0.0.1:1"
	converted := NodeRequestOf(mounted)
	if converted.Kind != NodeRequestRequest || converted.Request.Body != nethttp.NoBody {
		t.Fatalf("converted = %+v, want a request without a body", converted)
	}
	post := httptest.NewRequest("POST", "/json", strings.NewReader("{}"))
	post.Host = "127.0.0.1:1"
	if converted := NodeRequestOf(post); converted.Kind != NodeRequestRequest || converted.Request.Body == nethttp.NoBody {
		t.Fatalf("a POST's body is kept: %+v", converted)
	}
}

// Review S5 (a): the scope of a request whose client went away aborts with Pi's reason.
func TestReviewAClientThatDisconnectsAbortsTheScopeWithPisReason(t *testing.T) {
	entered := make(chan host.Ctx, 1)
	server := serving(t, []plugin{{"@app/slow", contributing(HttpContribution{Routes: []HttpRouteEntry{
		publicRoute("GET /slow", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			entered <- ctx
			<-ctx.Done()
			return HttpResponse{Kind: KindError, Status: 503}, nil
		}),
	}})}}, servedOptions{})
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(server.port))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("GET /slow HTTP/1.1\r\nHost: 127.0.0.1:" + strconv.Itoa(server.port) + "\r\n\r\n"))
	ctx := receive(t, entered, "the route")
	_ = conn.Close()
	receive(t, ctx.Done(), "the abort")
	if cause := context.Cause(ctx); cause == nil || cause.Error() != "http: the client disconnected" {
		t.Fatalf("cause = %v, want http: the client disconnected", cause)
	}
}

// Review S5 (b): net/http cancels a request's context when its handler returns; that is no disconnect once the response was
// written.
func TestReviewTheRequestContextEndingAfterTheResponseIsNoDisconnect(t *testing.T) {
	for _, finished := range []bool{false, true} {
		request := httptest.NewRequest("GET", "/who", nil)
		request.Host = "127.0.0.1:1"
		ctx, cancel := context.WithCancel(context.Background())
		incoming, refusal := nodeIncomingOf(request.WithContext(ctx))
		if refusal != nil {
			t.Fatal(refusal)
		}
		var mu sync.Mutex
		var reasons []error
		aborted := make(chan struct{}, 1)
		incoming.Cancelled(func(reason error) {
			mu.Lock()
			reasons = append(reasons, reason)
			mu.Unlock()
			aborted <- struct{}{}
		})
		incoming.finished.Store(finished)
		cancel()
		if finished {
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			if len(reasons) != 0 {
				t.Fatalf("a finished request aborted with %v", reasons)
			}
			mu.Unlock()
			continue
		}
		receive(t, aborted, "the abort")
		mu.Lock()
		if len(reasons) != 1 || reasons[0].Error() != "http: the client disconnected" {
			t.Fatalf("reasons = %v", reasons)
		}
		mu.Unlock()
	}
}

// Review S7: nothing follows the router once close ran.
func TestReviewTheFollowerRevokesNothingAfterClose(t *testing.T) {
	var mu sync.Mutex
	revoked := 0
	tn := hostsTunnel("app.tunnel", nil)
	server := serving(t, []plugin{{"@app/tunnel", tn.facet}}, servedOptions{
		noListen:   true,
		revokeHook: func(*Router) { mu.Lock(); revoked++; mu.Unlock() },
	})
	// A first resolution and a change while open: the follower runs.
	if status := httpStatus(server, "/x", httpLoopback); status != 404 {
		t.Fatalf("status = %d", status)
	}
	tn.name(t, server, "a.example.com")
	eventually(t, "the follower ran for the change", func() bool { mu.Lock(); defer mu.Unlock(); return revoked > 0 })
	server.http.Close(background)
	mu.Lock()
	before := revoked
	mu.Unlock()
	tn.name(t, server, "b.example.com")
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if revoked != before {
		t.Fatalf("the follower revoked %d times after close", revoked-before)
	}
}

// httpLoopback makes a request a loopback one for Fetch (its Host is set already by loopback).
func httpLoopback(*nethttp.Request) {}

// Review S8: a plugin socket's frames are read while its handler runs: its pongs keep the heartbeat from terminating it, and the
// messages that arrive meanwhile are delivered when the handler returned.
func TestReviewAPluginSocketIsReadWhileItsHandlerRuns(t *testing.T) {
	clock := newFakeClock()
	blocked := newGate()
	release := newGate()
	t.Cleanup(release.resolve)
	received := make(chan string, 4)
	server := serving(t, []plugin{{"@app/slow", contributing(HttpContribution{Sockets: []HttpSocketEntry{{
		Pattern: "/slow", Socket: HttpSocketOptions{Handler: &HttpSocket{Handle: func(socket *WebSocketHandle, _ HttpRequest, _ host.Ctx) error {
			socket.OnMessage(func(message Message) { received <- message.Text() })
			blocked.resolve()
			<-release.done()
			return nil
		}}},
	}}})}}, servedOptions{after: clock.afterFunc})
	client, err := dialSocket(server.port, "/slow", owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	pings := make(chan struct{}, 4)
	client.OnPing(func([]byte) { pings <- struct{}{} })
	client.Start()
	receive(t, blocked.done(), "the handler")
	eventually(t, "the heartbeat timer", func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return len(clock.timers) == 1
	})
	client.Send([]byte("while the handler runs"), false, nil)
	clock.advance(15 * time.Second)
	receive(t, pings, "the first ping")
	// The pong travels for real: the server reads it while its handler still runs, so the second deadline only pings again.
	time.Sleep(100 * time.Millisecond)
	clock.advance(15 * time.Second)
	receive(t, pings, "the second ping")
	if client.ReadyState() != WebSocketOpenState {
		t.Fatalf("the socket is %d: a handler that runs longer than the heartbeat lost its socket", client.ReadyState())
	}
	select {
	case message := <-received:
		t.Fatalf("a message reached the handler's listener before it returned: %q", message)
	default:
	}
	release.resolve()
	if got := receive(t, received, "the held message"); got != "while the handler runs" {
		t.Fatalf("message = %q", got)
	}
	client.Close(0, "")
}

// Review S9: a plugin's listeners get typed data.
func TestReviewAPluginSocketsListenersGetTypedMessages(t *testing.T) {
	got := make(chan Message, 2)
	server := serving(t, []plugin{{"@app/typed", contributing(HttpContribution{Sockets: []HttpSocketEntry{{
		Pattern: "/typed", Socket: HttpSocketOptions{Handler: &HttpSocket{Handle: func(socket *WebSocketHandle, _ HttpRequest, _ host.Ctx) error {
			socket.OnMessage(func(message Message) { got <- message })
			return nil
		}}},
	}}})}}, servedOptions{})
	client := mustSocketTo(t, server.port, "/typed", owner)
	client.Send([]byte("text"), false, nil)
	client.Send([]byte{1, 2}, true, nil)
	if first := receive(t, got, "the text message"); first.Binary || first.Text() != "text" {
		t.Fatalf("first = %+v", first)
	}
	if second := receive(t, got, "the binary message"); !second.Binary || len(second.Data) != 2 {
		t.Fatalf("second = %+v", second)
	}
	client.Close(0, "")
}

// Review S17: a send never queues behind the close frame.
func TestReviewASendRacingCloseNeverQueuesBehindTheCloseFrame(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*nethttp.Request) bool { return true }}
	for range 200 {
		sockets := make(chan *WebSocket, 1)
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			socket := newWebSocket(conn)
			socket.Start()
			sockets <- socket
		}))
		client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil) //nolint:bodyclose // the dialer documents its handshake response body as needing no close
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				if _, _, err := client.ReadMessage(); err != nil {
					return
				}
			}
		}()
		socket := receive(t, sockets, "the socket")
		var wg sync.WaitGroup
		var mu sync.Mutex
		var afterClose int
		for range 4 {
			wg.Go(func() {
				for range 20 {
					socket.Send([]byte("x"), false, func(err error) {
						if errors.Is(err, websocket.ErrCloseSent) {
							mu.Lock()
							afterClose++
							mu.Unlock()
						}
					})
				}
			})
		}
		wg.Go(func() { socket.Close(1000, "") })
		wg.Wait()
		eventually(t, "the socket closed", func() bool { return socket.ReadyState() == WebSocketClosedState || socket.BufferedAmount() == 0 })
		socket.Terminate()
		_ = client.Close()
		server.Close()
		mu.Lock()
		if afterClose != 0 {
			mu.Unlock()
			t.Fatalf("%d sends were queued behind the close frame", afterClose)
		}
		mu.Unlock()
	}
}

// Review S11: SameSite is a closed set; no value injects an attribute.
func TestReviewSameSiteIsAClosedSet(t *testing.T) {
	for _, each := range []struct {
		value SameSite
		want  string
	}{{"", "Strict"}, {SameSiteStrict, "Strict"}, {SameSiteLax, "Lax"}, {"None; Secure", "Strict"}, {"lax", "Strict"}} {
		if got := each.value.attribute(); got != each.want {
			t.Fatalf("SameSite(%q) = %q, want %q", each.value, got, each.want)
		}
	}
}

// Review: the statuses where Node's reason phrase is not Go's.
func TestReviewStatusTextIsNodes(t *testing.T) {
	for status, want := range map[int]string{
		200: "OK", 404: "Not Found", 413: "Payload Too Large", 414: "URI Too Long", 416: "Range Not Satisfiable",
		418: "I'm a Teapot", 422: "Unprocessable Entity", 500: "Internal Server Error", 509: "Bandwidth Limit Exceeded", 599: "",
	} {
		if got := statusText(status); got != want {
			t.Fatalf("statusText(%d) = %q, want %q", status, got, want)
		}
	}
}

// Review S10: Node's `extname` of a name that is only a dot and a word is "".
func TestReviewAFileNamedByItsExtensionHasNoExtension(t *testing.T) {
	for path, want := range map[string]string{
		"/x/.png": "application/octet-stream", "/x/a.PNG": "image/png", "/x/a.": "application/octet-stream",
		"/x/..": "application/octet-stream", "/x/a.b.mp3": "audio/mpeg", ".md": "application/octet-stream",
	} {
		if got := mimeOf(path); got != want {
			t.Fatalf("mimeOf(%q) = %q, want %q", path, got, want)
		}
	}
}

// Review S12: conflict messages quote the key as written, not as JSON.
func TestReviewConflictMessagesQuoteKeysAsWritten(t *testing.T) {
	handler := func(HttpRequest, host.Ctx) (HttpResponse, error) { return htmlOf("x"), nil }
	_, conflicts := routerCompose(t,
		routerEntry{"@app/a", HttpContribution{Routes: []HttpRouteEntry{RouteFunc(`GET /a"b\c`, handler)}}},
		routerEntry{"@app/b", HttpContribution{Routes: []HttpRouteEntry{RouteFunc(`GET /a"b\c`, handler)}}},
	)
	if len(conflicts) != 1 || conflicts[0].Message != `route "GET /a"b\c" is contributed twice` {
		t.Fatalf("conflicts = %+v", conflicts)
	}
}

// Review S13: the host is lowercased as ASCII only.
func TestReviewHostsAreLowercasedAsASCIIOnly(t *testing.T) {
	hosts := []string{"notei.example"}
	request := admissionRequest([]admissionHeader{{"host", "note\u0130.example"}})
	if _, ok := isRefused(admit(request, Connection{}, hosts, nil)); !ok {
		t.Fatal("Host: noteİ.example was admitted as notei.example")
	}
}

// Review S14 and S15: what the URL parser serializes as an origin.
func TestReviewOriginsAreWhatTheURLParserSerializes(t *testing.T) {
	for text, want := range map[string]bool{
		"https://[fe80::1%25eth0]": false, "https://[fe80::1%eth0]": false, "https://[::1]:8443": true,
		"https://a.0xbar": true, "https://a.0x1": false, "https://a.0x": false, "https://127.0.0.1": true,
	} {
		if got := isOrigin(text); got != want {
			t.Fatalf("isOrigin(%q) = %v, want %v", text, got, want)
		}
	}
}

// Pi's `socket.once("close", fn)` (Node's EventEmitter `once`) registers and arms its wrapper in one step: a close cannot
// reach the wrapper before it can remove itself. A close on another goroutine between the registration and the hand-back of
// the removal called a nil `removed` (and read it unsynchronised). The hook forces that interleaving.
func TestReviewAOnceListenerWhoseSocketClosesRightAfterItsRegistrationFiresOnceAndIsRemoved(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*nethttp.Request) bool { return true }}
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if _, err := upgrader.Upgrade(w, r, nil); err != nil {
			return
		}
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil) //nolint:bodyclose // the dialer documents its handshake response body as needing no close
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	socket := newWebSocket(conn)
	var calls atomic.Int32
	socket.attached = func() {
		socket.attached = nil
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			socket.closed(CloseEvent{Code: 1006})
		}()
		if got := receive(t, panicked, "the close"); got != nil {
			t.Errorf("the close that raced the registration panicked: %v", got)
		}
	}
	remove := socket.OnceClose(func(CloseEvent) { calls.Add(1) })
	if got := calls.Load(); got != 1 {
		t.Fatalf("the once listener ran %d times, want 1", got)
	}
	socket.mu.Lock()
	left := len(socket.listeners)
	socket.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d listeners left, want the once listener removed", left)
	}
	remove()
	socket.emit(link.WebSocketClose, CloseEvent{Code: 1006})
	if got := calls.Load(); got != 1 {
		t.Fatalf("the once listener ran %d times after a second close, want 1", got)
	}
}

// In Pi the hand-out of an upgrade and the keeping of its connection are one synchronous step, so a router change reaches every
// connection admitted by the router before it (`revoke` sees it). Here a change that ran while the upgrade was between its
// router check and its keep missed the connection: the server never ended it (soak F7's 30 s hang, `the server never ended the
// connection`).
func TestReviewARouterChangeBetweenAnUpgradesCheckAndItsKeepStillEndsTheConnection(t *testing.T) {
	entered := newGate()
	release := newGate()
	t.Cleanup(release.resolve)
	revoked := make(chan struct{}, 8)
	tunnel := plugin{"@app/tunnel", contributing(HttpContribution{Hosts: []string{"t.example"}})}
	server := serving(t, []plugin{tunnel}, servedOptions{
		revokeHook: func(router *Router) {
			if !slices.Contains(router.Hosts, "t.example") {
				revoked <- struct{}{}
			}
		},
		links: &LinksOptions{Path: "/chord", OptionsFunc: func(HttpRequest, data.Principal) (LinkOptions, error) {
			// After the checks against the router, before the socket is kept.
			entered.resolve()
			<-release.done()
			return LinkOptions{}, nil
		}},
	})
	type dialed struct {
		socket *clientSocket
		err    error
	}
	opening := httpAsync(func() dialed {
		socket, err := socketTo(server.port, "/chord", withHeaders(owner, map[string]string{"host": "t.example"}), 0)
		return dialed{socket, err}
	})
	receive(t, entered.done(), "the links options hook")
	server.reload(t)
	receive(t, revoked, "the revocation against the router without the host")
	server.http.revokeTasks.Wait()
	release.resolve()
	got := receive(t, opening, "the upgrade")
	if got.err != nil {
		t.Fatalf("the upgrade failed with %v", got.err)
	}
	receive(t, got.socket.closed, "the server ending the connection its host left")
}

// Node's `once` under an emission an earlier listener repeats: the wrapper runs once (its `fired` flag), and it left the
// listeners before its function ran (so the repeated emission finds nothing to run again).
func TestReviewAOnceListenerRunsOnceUnderAReentrantEmitAndLeavesBeforeItsFunctionRuns(t *testing.T) {
	socket := &WebSocket{state: WebSocketOpenState}
	reentered := false
	socket.on(link.WebSocketClose, func(data any) {
		if !reentered {
			reentered = true
			socket.emit(link.WebSocketClose, data)
		}
	})
	calls, attachedDuringFn := 0, -1
	socket.OnceClose(func(CloseEvent) {
		calls++
		socket.mu.Lock()
		attachedDuringFn = len(socket.listeners)
		socket.mu.Unlock()
	})
	socket.emit(link.WebSocketClose, CloseEvent{Code: 1006})
	if calls != 1 || attachedDuringFn != 1 {
		t.Fatalf("calls = %d (want 1), listeners while fn ran = %d (want 1: the once wrapper removed first)", calls, attachedDuringFn)
	}
}

// hijackGate is a response writer whose hijack, the step of an upgrade between its router check and its keep, waits for the
// test: a plugin socket has no `links.options` to hold it there.
type hijackGate struct {
	nethttp.ResponseWriter
	entered *gate
	release *gate
}

func (h hijackGate) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.entered.resolve()
	<-h.release.done()
	return h.ResponseWriter.(nethttp.Hijacker).Hijack()
}

// The plugin socket path of the test above: the handler of the socket left while the upgrade was between its router check and
// its keep.
func TestReviewARouterChangeBetweenAPluginSocketUpgradesCheckAndItsKeepStillEndsTheConnection(t *testing.T) {
	entered := newGate()
	release := newGate()
	t.Cleanup(release.resolve)
	revoked := make(chan struct{}, 8)
	sockets := plugin{"@app/sockets", contributing(HttpContribution{Sockets: []HttpSocketEntry{SocketFunc("/echo", noopSocket())}})}
	server := serving(t, []plugin{sockets}, servedOptions{
		noListen: true,
		revokeHook: func(router *Router) {
			if len(router.SocketHandlers) == 0 {
				revoked <- struct{}{}
			}
		},
	})
	listener := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		server.http.Upgrade(hijackGate{ResponseWriter: w, entered: entered, release: release}, r)
	}))
	t.Cleanup(listener.Close)
	port, err := strconv.Atoi(listener.URL[strings.LastIndex(listener.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}
	type dialed struct {
		socket *clientSocket
		err    error
	}
	opening := httpAsync(func() dialed {
		socket, err := socketTo(port, "/echo", owner, 0)
		return dialed{socket, err}
	})
	receive(t, entered.done(), "the handshake")
	server.reload(t)
	receive(t, revoked, "the revocation against the router without the handler")
	server.http.revokeTasks.Wait()
	release.resolve()
	got := receive(t, opening, "the upgrade")
	if got.err != nil {
		t.Fatalf("the upgrade failed with %v", got.err)
	}
	receive(t, got.socket.closed, "the server ending the connection its handler left")
}

// Pi's revoke runs at the delivery of its router, so a router delivered before an upgrade's check never ends what that check
// admitted. Here the revoke against V1 (host gone) was still running, and the one against V2 (host back) waiting, when an
// upgrade checked against V2: the connection stays, whether V1's revoke takes its snapshot before the keep (the recheck must
// not end it) or after it (the revoke must not end it).
func TestReviewAStaleRevokeNeverEndsAConnectionTheNewestRouterAdmits(t *testing.T) {
	for _, afterKeep := range []bool{false, true} {
		name := "the stale revoke snapshots before the keep"
		if afterKeep {
			name = "the stale revoke snapshots after the keep"
		}
		t.Run(name, func(t *testing.T) {
			hookV1, releaseV1 := newGate(), newGate()
			hookV2, releaseV2 := newGate(), newGate()
			entered, release := newGate(), newGate()
			t.Cleanup(releaseV1.resolve)
			t.Cleanup(releaseV2.resolve)
			t.Cleanup(release.resolve)
			var removedSeen atomic.Bool
			tunnel := plugin{"@app/tunnel", contributing(HttpContribution{Hosts: []string{"t.example"}})}
			server := serving(t, []plugin{tunnel}, servedOptions{
				revokeHook: func(router *Router) {
					if !slices.Contains(router.Hosts, "t.example") {
						removedSeen.Store(true)
						hookV1.resolve()
						<-releaseV1.done()
						return
					}
					if removedSeen.Load() {
						hookV2.resolve()
						<-releaseV2.done()
					}
				},
				links: &LinksOptions{Path: "/chord", OptionsFunc: func(HttpRequest, data.Principal) (LinkOptions, error) {
					entered.resolve()
					<-release.done()
					return LinkOptions{}, nil
				}},
			})
			handedOut := func() {
				eventually(t, "the hand-out finished", func() bool {
					server.http.upgrades.mu.Lock()
					defer server.http.upgrades.mu.Unlock()
					return server.http.upgrades.pending == 0
				})
			}
			server.reload(t)
			receive(t, hookV1.done(), "the revoke against V1 (host gone)")
			server.reload(t, tunnel)
			type dialed struct {
				socket *clientSocket
				err    error
			}
			opening := httpAsync(func() dialed {
				socket, err := socketTo(server.port, "/chord", withHeaders(owner, map[string]string{"host": "t.example"}), 0)
				return dialed{socket, err}
			})
			receive(t, entered.done(), "the links options hook (checked against V2)")
			if !afterKeep {
				releaseV1.resolve()
				receive(t, hookV2.done(), "the revoke against V2 begins, the one against V1 done")
			}
			release.resolve()
			got := receive(t, opening, "the upgrade")
			if got.err != nil {
				t.Fatalf("upgrade: %v", got.err)
			}
			if afterKeep {
				handedOut()
				releaseV1.resolve()
				receive(t, hookV2.done(), "the revoke against V2 begins, the one against V1 done")
			}
			releaseV2.resolve()
			server.http.revokeTasks.Wait()
			handedOut()
			open := server.http.upgrades.snapshot()
			if len(open) != 1 || open[0].socket.ReadyState() != WebSocketOpenState {
				t.Fatalf("the connection the newest router admits was ended: %d kept", len(open))
			}
		})
	}
}

// Pi's hand-out is one synchronous step, so every router delivered after an upgrade's check revokes its connection in turn: a
// host removed and then restored between the check and the keep ends it at the removal's delivery, though the newest router
// admits it again.
func TestReviewAHostRemovedAndRestoredBetweenAnUpgradesCheckAndItsKeepEndsTheConnection(t *testing.T) {
	entered, release := newGate(), newGate()
	t.Cleanup(release.resolve)
	gone, back := make(chan struct{}, 8), make(chan struct{}, 8)
	var sawGone atomic.Bool
	tunnel := plugin{"@app/tunnel", contributing(HttpContribution{Hosts: []string{"t.example"}})}
	server := serving(t, []plugin{tunnel}, servedOptions{
		revokeHook: func(router *Router) {
			if !slices.Contains(router.Hosts, "t.example") {
				sawGone.Store(true)
				gone <- struct{}{}
			} else if sawGone.Load() {
				back <- struct{}{}
			}
		},
		links: &LinksOptions{Path: "/chord", OptionsFunc: func(HttpRequest, data.Principal) (LinkOptions, error) {
			entered.resolve()
			<-release.done()
			return LinkOptions{}, nil
		}},
	})
	type dialed struct {
		socket *clientSocket
		err    error
	}
	opening := httpAsync(func() dialed {
		socket, err := socketTo(server.port, "/chord", withHeaders(owner, map[string]string{"host": "t.example"}), 0)
		return dialed{socket, err}
	})
	receive(t, entered.done(), "the upgrade checked against the router with the host")
	server.reload(t)
	receive(t, gone, "the revoke against the router without the host")
	server.http.revokeTasks.Wait()
	server.reload(t, tunnel)
	receive(t, back, "the revoke against the router with the host back")
	server.http.revokeTasks.Wait()
	release.resolve()
	got := receive(t, opening, "the upgrade")
	if got.err != nil {
		t.Fatalf("upgrade: %v", got.err)
	}
	receive(t, got.socket.closed, "the server ending the connection at the removal of its host")
}

// serverSideSocket is a WebSocket over a connection to a server that upgrades and idles.
func serverSideSocket(t *testing.T) *WebSocket {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*nethttp.Request) bool { return true }}
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = upgrader.Upgrade(w, r, nil)
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil) //nolint:bodyclose // the dialer documents its handshake response body as needing no close
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return newWebSocket(conn)
}

// An upgrade counts the deliveries (V0) before it reads its router; V1 (host gone) is delivered meanwhile and V2 (host back) is
// what the check read. Pi's revoke of V1 ran before that check read V2, so it ends nothing; had the check read V0, V1's revoke
// ends the connection at its delivery.
func TestReviewAnUpgradeIsJudgedOnlyByTheRoutersDeliveredAfterTheRouterItRead(t *testing.T) {
	router := func(order uint64, host ...string) *Router {
		return &Router{Hosts: host, SocketHandlers: map[*HttpSocket]struct{}{}, order: order}
	}
	for _, read := range []string{"V0", "V2"} {
		t.Run("the check read "+read, func(t *testing.T) {
			u := NewUpgrades()
			v0, v1, v2 := router(1, "t.example"), router(2), router(3, "t.example")
			u.delivered(v0)
			seen := u.admitting()
			defer u.settled(seen)
			u.delivered(v1)
			u.delivered(v2)
			socket := serverSideSocket(t)
			checked := map[string]*Router{"V0": v0, "V2": v2}[read]
			connection := &upgraded{socket: socket, allowed: "t.example", seal: func() {}, checked: checked.order}
			u.keep(connection)
			u.recheck(connection)
			if ended := socket.ReadyState() == WebSocketClosedState; ended != (read == "V0") {
				t.Fatalf("after reading %s the connection ended = %v", read, ended)
			}
		})
	}
}

// The router the component reads and the one the follower delivers are the same composition (its order is what an upgrade's
// connection is judged from).
func TestReviewTheRouterTheComponentReadsIsTheOneTheFollowerDelivers(t *testing.T) {
	var last atomic.Pointer[Router]
	gone := make(chan struct{}, 8)
	tunnel := plugin{"@app/tunnel", contributing(HttpContribution{Hosts: []string{"t.example"}})}
	server := serving(t, []plugin{tunnel}, servedOptions{
		noListen: true,
		revokeHook: func(router *Router) {
			last.Store(router)
			if !slices.Contains(router.Hosts, "t.example") {
				gone <- struct{}{}
			}
		},
	})
	if status := httpStatus(server, "/x", httpLoopback); status != 404 {
		t.Fatalf("status = %d", status)
	}
	server.reload(t)
	receive(t, gone, "the revoke against the router without the host")
	server.http.revokeTasks.Wait()
	set, err := server.http.registriesNow()
	if err != nil {
		t.Fatal(err)
	}
	if got := set.routerNow(); got != last.Load() {
		t.Fatal("the router the component reads is not the one the follower delivered")
	}
}

// The deliveries an upgrade needs are kept only while it is in flight: its own settling, and every delivery that follows one,
// drop the rest (a leak would cost one router per reload for the life of the component).
func TestReviewTheDeliveriesKeptForUpgradesAreBoundedByTheOnesInFlight(t *testing.T) {
	u := NewUpgrades()
	router := func() *Router { return &Router{SocketHandlers: map[*HttpSocket]struct{}{}} }
	u.delivered(router())
	seen := u.admitting()
	const deliveries = 1000
	for range deliveries {
		u.delivered(router())
	}
	if got := len(u.history); got != deliveries+1 {
		t.Fatalf("in flight across %d deliveries the history holds %d, want %d", deliveries, got, deliveries+1)
	}
	u.settled(seen)
	if len(u.history) != 1 || len(u.inflight) != 0 {
		t.Fatalf("after settled the history holds %d and %d are in flight, want 1 and 0", len(u.history), len(u.inflight))
	}
	for range deliveries {
		each := u.admitting()
		u.delivered(router())
		u.settled(each)
	}
	if len(u.history) != 1 || len(u.inflight) != 0 {
		t.Fatalf("after %d admit, deliver, settle rounds the history holds %d and %d are in flight, want 1 and 0", deliveries, len(u.history), len(u.inflight))
	}
}

// The router an upgrade's check read can be composed and still undelivered when the connection is kept (the check composed it
// on read). The deliveries composed before it are older than the check (Pi's revoke of them ran before it): they judge
// nothing. One composed after it judges the connection, whether or not the router the check read is ever delivered.
func TestReviewAnUpgradeWhoseCheckReadAnUndeliveredRouterIsJudgedOnlyByLaterCompositions(t *testing.T) {
	router := func(order uint64, host ...string) *Router {
		return &Router{Hosts: host, SocketHandlers: map[*HttpSocket]struct{}{}, order: order}
	}
	for _, laterGone := range []bool{false, true} {
		name := "only an older composition was delivered since the count"
		if laterGone {
			name = "a later composition without the host was delivered before the keep"
		}
		t.Run(name, func(t *testing.T) {
			u := NewUpgrades()
			u.delivered(router(1, "t.example"))
			seen := u.admitting()
			defer u.settled(seen)
			u.delivered(router(2))
			read := router(3, "t.example")
			if laterGone {
				u.delivered(router(4))
			}
			socket := serverSideSocket(t)
			connection := &upgraded{socket: socket, allowed: "t.example", seal: func() {}, checked: read.order}
			u.keep(connection)
			u.recheck(connection)
			if ended := socket.ReadyState() == WebSocketClosedState; ended != laterGone {
				t.Fatalf("the connection ended = %v, want %v", ended, laterGone)
			}
		})
	}
}

// Every upgrade that was in flight is settled once it is over, handed out or refused, so the deliveries kept for it are
// released (the component's own `admitting` and `settled`, not the Upgrades' alone).
func TestReviewAnUpgradeIsSettledWhetherItWasHandedOutOrRefused(t *testing.T) {
	sockets := plugin{"@app/sockets", contributing(HttpContribution{Sockets: []HttpSocketEntry{SocketFunc("/echo", noopSocket())}})}
	server := serving(t, []plugin{sockets}, servedOptions{})
	mustRefused(t, server.port, "/nowhere", owner, 404)
	socket := mustSocketTo(t, server.port, "/echo", owner)
	t.Cleanup(func() { socket.Close(0, "") })
	eventually(t, "every upgrade settled and its deliveries released", func() bool {
		server.http.upgrades.mu.Lock()
		defer server.http.upgrades.mu.Unlock()
		return len(server.http.upgrades.inflight) == 0 && len(server.http.upgrades.history) == 1
	})
}

// Pi's HTTP component reads the router and the authenticators (`registries.router.value`) on the one JavaScript thread, between
// the turns of the host's loop, so a read never meets a reload composing the registry. Requests and upgrades here run on
// goroutines of their own: their reads must be sections of the host's loop too, or they recompose the registry beside a commit
// (data races, and a reload that fails with "reload inside a commit or a derive compute").
func TestReviewRequestsAndUpgradesReadTheRegistriesWhileReloadsComposeThem(t *testing.T) {
	sockets := plugin{"@app/sockets", contributing(HttpContribution{Sockets: []HttpSocketEntry{SocketFunc("/echo", noopSocket())}})}
	tunnel := plugin{"@app/tunnel", contributing(HttpContribution{Hosts: []string{"t.example"}})}
	// A route for the requests to reach their authenticators, and an authenticator the reloads add and remove in turn.
	routes := plugin{"@app/routes", contributing(HttpContribution{Routes: []HttpRouteEntry{
		publicRoute("GET /x", func(HttpRequest, host.Ctx) (HttpResponse, error) { return htmlOf("x"), nil }),
	}})}
	extra := plugin{"@app/extra-auth", authenticating(func(context.Context, HttpRequest) (data.Principal, error) { return nil, nil })}
	server := serving(t, []plugin{sockets, tunnel, routes}, servedOptions{})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	running := func() bool {
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}
	for range 4 {
		wg.Go(func() {
			for running() {
				_ = server.http.Fetch(loopback("/x"), loopbackConnection).Body.Close()
			}
		})
	}
	wg.Go(func() {
		for running() {
			if socket, err := socketTo(server.port, "/echo", owner, 0); err == nil {
				socket.Close(0, "")
			}
		}
	})
	for i := range 40 {
		if i%2 == 0 {
			server.reload(t, sockets, routes, extra)
		} else {
			server.reload(t, sockets, tunnel, routes)
		}
	}
	close(stop)
	wg.Wait()
}
