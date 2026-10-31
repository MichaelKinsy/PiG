package http

import (
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Tests of Chord's own listener (the listener's native): request targets (origin-form only; a malformed or double-slash path
// never crashes or reroutes), raw `Host` headers, the request's scope lasting until its response is flushed (and `close`
// waiting for it), and a client's disconnect aborting the scope. Ports packages/chord/test/next-node/http/node.test.ts.
//
// Not ported: "makes a Web Request only when something reads the headers or the body" counts `new Request` calls
// (designed out: Go's *net/http.Request exists when the server hands it over, there is no lazily made Web object).
//
// Forced difference (node.go): net/http refuses two `Host` headers itself, 400, where Node's parser keeps them and the
// component answers 403; the component's 403 for them is covered through Fetch (TestFetchRefusesTwoHostHeaders).

// echo is a plugin answering its path and params, under `GET /who` and `GET /n/:name`.
var echoPlugin = plugin{"@app/echo", contributing(HttpContribution{Routes: []HttpRouteEntry{
	RouteFunc("GET /who", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) { return jsonOf(request.Path()), nil }),
	RouteFunc("GET /n/:name", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
		params := map[string]any{}
		for name, value := range request.Params() {
			params[name] = value
		}
		return jsonOf(params), nil
	}),
}})}

// Astra's build review, Fable's security audit C1: `GET //[` threw out of the request handler and ended the process.
func TestTargetsAnswerAMalformedOrDoubleSlashPathWithoutCrashingOrReroutingIt(t *testing.T) {
	server := serving(t, []plugin{echoPlugin}, servedOptions{})
	host := "Host: 127.0.0.1:" + strconv.Itoa(server.port)
	get := func(target string) (string, string) {
		return rawExchange(t, server.port, "GET "+target+" HTTP/1.1\r\n"+host+"\r\nConnection: close\r\n\r\n")
	}
	expect := func(target, status string) {
		t.Helper()
		if got, _ := get(target); got != status {
			t.Fatalf("GET %s: %q, want %q", target, got, status)
		}
	}
	expect("//[", "HTTP/1.1 404 Not Found")
	expect("//evil.example/who", "HTTP/1.1 404 Not Found")
	if status, body := get("/a/../who"); status != "HTTP/1.1 200 OK" || body != `"/who"` {
		t.Fatalf("/a/../who: %q %q", status, body)
	}
	// The URL parser resolves `%2e%2e` as a dot segment: `/n/%2e%2e` is `/`.
	expect("/n/%2e%2e", "HTTP/1.1 404 Not Found")
	expect("/n/a%5Cb", "HTTP/1.1 400 Bad Request")
	expect("/n/..%2F..%2Fsecret", "HTTP/1.1 400 Bad Request")
	expect("/who", "HTTP/1.1 200 OK")
}

func TestTargetsRefuseAnAbsoluteFormTargetTwoHostHeadersAndNone(t *testing.T) {
	server := serving(t, []plugin{echoPlugin}, servedOptions{})
	host := "Host: 127.0.0.1:" + strconv.Itoa(server.port)
	status := func(text string) string {
		line, _ := rawExchange(t, server.port, text)
		return line
	}
	if got := status("GET http://evil.example/who HTTP/1.1\r\n" + host + "\r\nConnection: close\r\n\r\n"); got != "HTTP/1.1 403 Forbidden" {
		t.Fatalf("absolute-form: %q", got)
	}
	// net/http answers two Hosts itself: 400, where Node's parser lets the component refuse it 403.
	if got := status("GET /who HTTP/1.1\r\n" + host + "\r\nHost: evil.example\r\nConnection: close\r\n\r\n"); got != "HTTP/1.1 400 Bad Request" {
		t.Fatalf("two Hosts: %q", got)
	}
	// net/http answers an HTTP/1.0 request in HTTP/1.0 (Node always writes HTTP/1.1): the status is the one that counts.
	if got := status("GET /who HTTP/1.0\r\n\r\n"); got != "HTTP/1.0 403 Forbidden" {
		t.Fatalf("no Host: %q", got)
	}
}

func TestFetchRefusesTwoHostHeaders(t *testing.T) {
	server := serving(t, []plugin{echoPlugin}, servedOptions{noListen: true})
	request := loopback("/who", func(r *nethttp.Request) {
		r.Header["Host"] = []string{"127.0.0.1:8788", "evil.example"}
	})
	if got := server.http.Fetch(request, loopbackConnection).StatusCode; got != 403 {
		t.Fatalf("two Hosts = %d, want 403", got)
	}
}

func TestTargetsRefuseAHostWithUserinfo403AndAMethodAWebRequestCannotCarry501(t *testing.T) {
	server := serving(t, []plugin{echoPlugin}, servedOptions{})
	port := strconv.Itoa(server.port)
	userinfo, _ := rawExchange(t, server.port, "GET /who HTTP/1.1\r\nHost: attacker@127.0.0.1:"+port+"\r\nConnection: close\r\n\r\n")
	trace, _ := rawExchange(t, server.port, "TRACE /who HTTP/1.1\r\nHost: 127.0.0.1:"+port+"\r\nConnection: close\r\n\r\n")
	// `TRACK` never reaches Chord's own handler in Node (its parser refuses it); `CONNECT` goes to a `connect` event. net/http
	// refuses a `Host` with userinfo itself (400 where Node's parser lets the component answer 403: TestNodeRequestRefuses...).
	if !strings.HasPrefix(userinfo, "HTTP/1.1 400 Bad Request") || trace != "HTTP/1.1 501 Not Implemented" {
		t.Fatalf("userinfo %q, trace %q", userinfo, trace)
	}
}

// Sol's recheck N6: a hand-made `Request` threw unhandled on `Host: [` or an absolute-form target (the process ended) and
// kept only the first of two `Host`s.
func TestMountingConvertsWithTheListenersChecksNeverPanickingBadTargetsUserinfoTRACERefused(t *testing.T) {
	server := serving(t, []plugin{echoPlugin}, servedOptions{noListen: true})
	own := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		converted := NodeRequestOf(r)
		if converted.Refused != nil {
			w.WriteHeader(converted.Refused.Status)
			return
		}
		response := server.http.Fetch(converted.Request, converted.Connection)
		for name, values := range response.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
		response.Body.Close()
	}))
	defer own.Close()
	port := own.Listener.Addr().(*net.TCPAddr).Port
	host := "Host: 127.0.0.1:" + strconv.Itoa(port)
	get := func(text string) (string, string) {
		return rawExchange(t, port, text+"Connection: close\r\n\r\n")
	}
	check := func(text, wantStatus string) {
		t.Helper()
		if got, _ := get(text); got != wantStatus {
			t.Fatalf("%q: %q, want %q", text, got, wantStatus)
		}
	}
	if status, body := get("GET /who HTTP/1.1\r\n" + host + "\r\n"); status != "HTTP/1.1 200 OK" || body != `"/who"` {
		t.Fatalf("GET /who: %q %q", status, body)
	}
	check("GET /who HTTP/1.1\r\nHost: [\r\n", "HTTP/1.1 400 Bad Request")
	check("GET http://evil.example/who HTTP/1.1\r\n"+host+"\r\n", "HTTP/1.1 403 Forbidden")
	// Two `Host`s: net/http answers 400 itself (see the file comment); the component's 403 is TestFetchRefusesTwoHostHeaders.
	check("GET /who HTTP/1.1\r\n"+host+"\r\nHost: evil.example\r\n", "HTTP/1.1 400 Bad Request")
	// Sol's second recheck A: both threw out of the conversion; the app's callback ended the process.
	check("TRACE /who HTTP/1.1\r\n"+host+"\r\n", "HTTP/1.1 501 Not Implemented")
	// A `Host` with userinfo: net/http's server refuses it itself, 400 (the 403 is TestNodeRequestRefusesWhatTheListenerRefuses).
	if got, _ := get("GET /who HTTP/1.1\r\nHost: attacker@127.0.0.1:" + strconv.Itoa(port) + "\r\n"); !strings.HasPrefix(got, "HTTP/1.1 400 Bad Request") {
		t.Fatalf("userinfo: %q", got)
	}
	check("GET /who HTTP/1.1\r\n"+host+"\r\n", "HTTP/1.1 200 OK")
}

// Sol's build review: the scope sealed, and `close` stopped waiting, before a large response was flushed.
func TestTheRequestsScopeLastsUntilTheResponseIsFlushedCloseWaitsForIt(t *testing.T) {
	const size = 32 * 1024 * 1024
	scope := make(chan host.Ctx, 1)
	body := strings.Repeat("x", size)
	server := serving(t, []plugin{{"@app/large", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /large", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
			scope <- ctx
			return htmlOf(body), nil
		}),
	}})}}, servedOptions{closeTimeout: 2 * time.Second})
	client := &nethttp.Client{Transport: &nethttp.Transport{DisableKeepAlives: true}}
	response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(server.port) + "/large")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	ctx := receive(t, scope, "the route")
	time.Sleep(20 * time.Millisecond)
	sealedBeforeFlush := ctx.Err() != nil
	closing := make(chan struct{})
	go func() {
		server.http.Close(background)
		close(closing)
	}()
	time.Sleep(20 * time.Millisecond)
	length, err := io.Copy(io.Discard, response.Body)
	if err != nil {
		t.Fatal(err)
	}
	receive(t, closing, "close")
	if sealedBeforeFlush || length != size {
		t.Fatalf("sealedBeforeFlush = %v, length = %d", sealedBeforeFlush, length)
	}
}

func TestTheRequestsScopeIsAbortedWhenTheClientDisconnects(t *testing.T) {
	entered := make(chan host.Ctx, 1)
	server := serving(t, []plugin{{"@app/slow", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /slow", func(_ HttpRequest, ctx host.Ctx) (HttpResponse, error) {
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
	conn.Close()
	receive(t, ctx.Done(), "the abort")
	if ctx.Err() == nil {
		t.Fatal("the scope is not aborted")
	}
}

// What net/http's server refuses before the component sees a request (a `Host` with userinfo, two `Host`s) Pi's conversion and
// listener answer themselves: NodeRequestOf and Fetch are the same checks on a request handed over by any server.
func TestNodeRequestRefusesWhatTheListenerRefuses(t *testing.T) {
	request := func(method, target, hostName string) *nethttp.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.Method, r.RequestURI, r.Host = method, target, hostName
		return r
	}
	for _, each := range []struct {
		name    string
		request *nethttp.Request
		status  int
	}{
		{"an absolute-form target", request("GET", "http://evil.example/who", "127.0.0.1:1"), 403},
		{"a Host with userinfo", request("GET", "/who", "attacker@127.0.0.1:1"), 403},
		{"a Host that does not parse", request("GET", "/who", "["), 400},
		{"TRACE", request("TRACE", "/who", "127.0.0.1:1"), 501},
		{"CONNECT", request("CONNECT", "/who", "127.0.0.1:1"), 501},
		{"TRACK", request("TRACK", "/who", "127.0.0.1:1"), 501},
	} {
		converted := NodeRequestOf(each.request)
		if converted.Refused == nil || converted.Refused.Status != each.status {
			t.Fatalf("%s: %+v, want refused %d", each.name, converted, each.status)
		}
	}
	converted := NodeRequestOf(request("GET", "/a/../who?x=1", "127.0.0.1:8788"))
	if converted.Refused != nil || converted.Request.URL.Path != "/who" || converted.Request.URL.RawQuery != "x=1" || converted.Request.Header.Get("Host") != "127.0.0.1:8788" {
		t.Fatalf("converted = %+v", converted)
	}
}
