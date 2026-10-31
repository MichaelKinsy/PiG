package http

import (
	"bufio"
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptrace"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Tests of requests (slice 10 §2, D4): the summary a route gets, `json` and `body` with their limits and statuses (400, 413,
// an `HttpFailure` escaping a route answering its status), and a body nobody read (drained or closed). Ports
// packages/chord/test/next-node/http/request.test.ts.

func TestRequestHasItsMethodPathParamsQueryHeadersOriginProvenanceAndNonce(t *testing.T) {
	var mu sync.Mutex
	var seen HttpRequest
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /notes/:id", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
			mu.Lock()
			seen = request
			mu.Unlock()
			return jsonOf(nil), nil
		}),
	}})}}, servedOptions{})
	mustSend(t, server.port, "GET", "/notes/a%20b?x=1&x=2", sendOptions{headers: map[string]string{"X-Thing": "t"}})
	mu.Lock()
	defer mu.Unlock()
	if seen.Method() != "GET" || seen.Path() != "/notes/a%20b" || seen.Params()["id"] != "a b" || len(seen.Params()) != 1 {
		t.Fatalf("request = %s %s %v", seen.Method(), seen.Path(), seen.Params())
	}
	if want := "http://127.0.0.1:" + strconv.Itoa(server.port); seen.Origin() != want {
		t.Fatalf("origin = %q, want %q", seen.Origin(), want)
	}
	if seen.Provenance() != (Provenance{Kind: ProvenanceLocal}) {
		t.Fatalf("provenance = %+v", seen.Provenance())
	}
	if got := seen.Query()["x"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("query x = %v", got)
	}
	if thing, _ := HeaderGet(seen.Headers(), "x-thing"); thing != "t" {
		t.Fatalf("x-thing = %q", thing)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9+/]{22}==$`).MatchString(seen.Nonce()) {
		t.Fatalf("nonce = %q", seen.Nonce())
	}
}

// reading is a server whose `POST /json` answers the JSON it read (limit 16), `POST /body` the bytes it read (limit 16).
func reading(t *testing.T) *testServer {
	return serving(t, []plugin{{"@app/a", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("POST /json", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
			value, err := request.JSON(16)
			if err != nil {
				return HttpResponse{}, err
			}
			return jsonOf(value), nil
		}),
		RouteFunc("POST /body", func(request HttpRequest, _ host.Ctx) (HttpResponse, error) {
			bytes := 0
			for chunk, err := range request.Body(16) {
				if err != nil {
					return HttpResponse{}, err
				}
				bytes += len(chunk)
			}
			return jsonOf(float64(bytes)), nil
		}),
	}})}}, servedOptions{})
}

func TestBodiesParseJSONWithinItsLimitMalformedIs400LargerIs413(t *testing.T) {
	server := reading(t)
	port := server.port
	if got := mustSend(t, port, "POST", "/json", sendOptions{body: `{"a":1}`}).text(); got != `{"a":1}` {
		t.Fatalf("json = %q", got)
	}
	if got := mustSend(t, port, "POST", "/json", sendOptions{body: "{nope"}).status; got != 400 {
		t.Fatalf("malformed = %d", got)
	}
	if got := mustSend(t, port, "POST", "/json", sendOptions{body: `{"long":"` + strings.Repeat("x", 40) + `"}`}).status; got != 413 {
		t.Fatalf("declared too large = %d", got)
	}
	chunked := mustSend(t, port, "POST", "/body", sendOptions{headers: map[string]string{"transfer-encoding": "chunked"}, body: strings.Repeat("y", 40)})
	if chunked.status != 413 {
		t.Fatalf("streamed too large = %d", chunked.status)
	}
	if got := mustSend(t, port, "POST", "/body", sendOptions{body: strings.Repeat("y", 10)}).text(); got != "10" {
		t.Fatalf("body bytes = %q", got)
	}
	if errs := server.errors.all(); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
}

// Sol's and Astra's build reviews: an unfinished chunked upload past the limit got no answer.
func TestBodiesAnswer413BeforeAChunkedUploadPastTheLimitHasEnded(t *testing.T) {
	server := reading(t)
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(server.port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	text := strings.Repeat("x", 40)
	chunk := `"` + text
	_, err = io.WriteString(conn, "POST /json HTTP/1.1\r\nHost: 127.0.0.1:"+strconv.Itoa(server.port)+"\r\nTransfer-Encoding: chunked\r\n\r\n"+strconv.FormatInt(int64(len(chunk)), 16)+"\r\n"+chunk+"\r\n")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(within))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 413 ") {
		t.Fatalf("status line = %q", line)
	}
}

func TestBodiesDrainASmallBodyNobodyReadAndKeepTheConnectionALargeOneClosesIt(t *testing.T) {
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("POST /ignore", func(HttpRequest, host.Ctx) (HttpResponse, error) { return jsonOf(float64(1)), nil }),
	}})}}, servedOptions{})
	transport := &nethttp.Transport{MaxConnsPerHost: 1}
	client := &nethttp.Client{Transport: transport}
	defer transport.CloseIdleConnections()
	type posted struct {
		connection string
		closed     bool
		reused     bool
	}
	post := func(body string) posted {
		var reused bool
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
		request, err := nethttp.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(server.port)+"/ignore", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return posted{connection: response.Header.Get("Connection"), closed: response.Close, reused: reused}
	}
	first := post(strings.Repeat("x", 1000))
	second := post(strings.Repeat("x", 1000))
	if first.connection != "keep-alive" || !second.reused {
		t.Fatalf("first = %+v, second = %+v: the connection must stay", first, second)
	}
	large := post(strings.Repeat("x", 100_000))
	// net/http's client reports the `Connection: close` it read as Response.Close.
	if !large.closed {
		t.Fatalf("large = %+v, want Connection: close", large)
	}
}
