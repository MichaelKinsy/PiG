package http

import (
	"bytes"
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Tests of responses (slice 10 §2, D5, R6; security baseline (4), (6)): every kind written by a running server, the
// response's headers over the default ones (an invalid one reported, 500), ranges (206, 416, multi-range), `HEAD`,
// attachments and inline bodies, safe download names, bodies without `size`, a short or throwing read, a disconnect
// stopping the reads. Ports packages/chord/test/next-node/http/response.test.ts.

// answering is a server answering `GET /r` with response.
func answering(t *testing.T, response HttpResponse) *testServer {
	return serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /r", func(HttpRequest, host.Ctx) (HttpResponse, error) { return response, nil }),
	}})}}, servedOptions{})
}

// reads records the reads of a `bytes` response.
type reads struct {
	mu    sync.Mutex
	items [][2]int64
}

func (r *reads) add(offset, length int64) {
	r.mu.Lock()
	r.items = append(r.items, [2]int64{offset, length})
	r.mu.Unlock()
}

func (r *reads) all() [][2]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]int64(nil), r.items...)
}

func (r *reads) reset() {
	r.mu.Lock()
	r.items = nil
	r.mu.Unlock()
}

// bytesOf is a `bytes` response over data that records its reads.
func bytesOf(data []byte, extra func(*HttpResponse)) (HttpResponse, *reads) {
	recorded := &reads{}
	size := int64(len(data))
	response := HttpResponse{
		Kind: KindBytes, Mime: "application/octet-stream", Size: &size,
		Read: func(offset int64, length int, _ host.Ctx) ([]byte, error) {
			recorded.add(offset, int64(length))
			end := offset + int64(length)
			if end > int64(len(data)) {
				end = int64(len(data))
			}
			return data[offset:end], nil
		},
	}
	if extra != nil {
		extra(&response)
	}
	return response, recorded
}

func TestDescriptionsWriteJSONHTMLRedirectAndErrorTheDefaultHeadersFirstTheResponsesReplacingSameNamedOnes(t *testing.T) {
	type testCase struct {
		response    HttpResponse
		status      int
		body        string
		contentType string
	}
	cases := []testCase{
		{jsonOf(map[string]any{"a": float64(1)}), 200, `{"a":1}`, "application/json; charset=utf-8"},
		{HttpResponse{Kind: KindHTML, Body: "<p>x</p>", Status: 201, Headers: []ResponseHeader{HeaderOf("content-security-policy", "own")}}, 201, "<p>x</p>", "text/html; charset=utf-8"},
		{HttpResponse{Kind: KindRedirect, To: "/next", Status: 303}, 303, "", ""},
		{HttpResponse{Kind: KindError, Status: 418}, 418, "I'm a Teapot", "text/plain; charset=utf-8"},
		{HttpResponse{Kind: KindError, Status: 409, Message: ptr("taken")}, 409, "taken", "text/plain; charset=utf-8"},
	}
	for _, each := range cases {
		server := answering(t, each.response)
		got := mustSend(t, server.port, "GET", "/r", sendOptions{})
		if got.status != each.status || got.text() != each.body || got.header("content-type") != each.contentType {
			t.Fatalf("%v: got %d %q %q, want %d %q %q", each.response.Kind, got.status, got.text(), got.header("content-type"), each.status, each.body, each.contentType)
		}
		if got.header("cache-control") != "no-store" || got.header("x-content-type-options") != "nosniff" {
			t.Fatalf("default headers = %v", got.headers)
		}
		want := regexp.MustCompile(`^default-src 'none'`)
		if each.response.Headers != nil {
			want = regexp.MustCompile(`^own$`)
		}
		if !want.MatchString(got.header("content-security-policy")) {
			t.Fatalf("csp = %q, want %s", got.header("content-security-policy"), want)
		}
		if each.response.Kind == KindRedirect && got.header("location") != "/next" {
			t.Fatalf("location = %q", got.header("location"))
		}
		server.close()
	}
}

func ptr[T any](value T) *T { return &value }

func TestDescriptionsAnswer500ForAHeaderWithACROrLFAndAValueThatIsNoJSONReportedResponseFailed(t *testing.T) {
	server := serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /header", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			return HttpResponse{Kind: KindJSON, Value: float64(1), Headers: []ResponseHeader{HeaderOf("X-Split", "a\r\nSet-Cookie: x=1")}}, nil
		}),
		// A value with no JSON form (Pi's BigInt).
		RouteFunc("GET /bigint", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			return HttpResponse{Kind: KindJSON, Value: map[string]any{"n": make(chan int)}}, nil
		}),
	}})}}, servedOptions{})
	for _, path := range []string{"/header", "/bigint"} {
		got := mustSend(t, server.port, "GET", path, sendOptions{})
		if got.status != 500 || len(got.headers.Values("Set-Cookie")) != 0 || got.header("x-content-type-options") != "nosniff" {
			t.Fatalf("%s: %d %v", path, got.status, got.headers)
		}
	}
	errs := server.errors.all()
	if len(errs) != 2 || errs[0].Kind != ErrResponseFailed || errs[0].Pattern != "GET /header" || errs[1].Kind != ErrResponseFailed || errs[1].Pattern != "GET /bigint" {
		t.Fatalf("errors = %+v", errs)
	}
}

func bigData() []byte { return bytes.Repeat([]byte("0123456789"), 60_000) }

func TestBytesSendContentLengthRead256KiBPiecesAnswerASingleRange206AndAnUnsatisfiableOne416(t *testing.T) {
	data := bigData()
	response, recorded := bytesOf(data, func(r *HttpResponse) { r.Ranges = true })
	server := answering(t, response)
	whole := mustSend(t, server.port, "GET", "/r", sendOptions{})
	if whole.status != 200 || whole.header("content-length") != "600000" || whole.header("accept-ranges") != "bytes" {
		t.Fatalf("whole = %d %v", whole.status, whole.headers)
	}
	if !bytes.Equal(whole.body, data) {
		t.Fatal("the body differs")
	}
	want := [][2]int64{{0, 262144}, {262144, 262144}, {524288, 75712}}
	if got := recorded.all(); !equalReads(got, want) {
		t.Fatalf("reads = %v, want %v", got, want)
	}
	ranged := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"range": "bytes=10-19"}})
	if ranged.status != 206 || ranged.text() != "0123456789" || ranged.header("content-range") != "bytes 10-19/600000" {
		t.Fatalf("range = %d %q %q", ranged.status, ranged.text(), ranged.header("content-range"))
	}
	suffix := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"range": "bytes=-3"}})
	if suffix.status != 206 || suffix.text() != "789" {
		t.Fatalf("suffix = %d %q", suffix.status, suffix.text())
	}
	past := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"range": "bytes=600000-"}})
	if past.status != 416 || past.header("content-range") != "bytes */600000" {
		t.Fatalf("past = %d %q", past.status, past.header("content-range"))
	}
	multi := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"range": "bytes=0-1,5-6"}})
	if multi.status != 200 || len(multi.body) != 600000 {
		t.Fatalf("multi = %d %d", multi.status, len(multi.body))
	}
}

func equalReads(a, b [][2]int64) bool {
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

func TestBytesIgnoreRangesUnlessAskedHEADSendsHeadersOnlyAndReadsNothing(t *testing.T) {
	response, recorded := bytesOf(bigData(), nil)
	server := answering(t, response)
	ranged := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"range": "bytes=0-1"}})
	if ranged.status != 200 || ranged.header("accept-ranges") != "" {
		t.Fatalf("ranged = %d %v", ranged.status, ranged.headers)
	}
	recorded.reset()
	head := mustSend(t, server.port, "HEAD", "/r", sendOptions{})
	if head.status != 200 || head.header("content-length") != "600000" || len(head.body) != 0 || len(recorded.all()) != 0 {
		t.Fatalf("head = %d %v %d %v", head.status, head.headers, len(head.body), recorded.all())
	}
}

func TestBytesNameADownloadWithAnASCIIFallbackAndTheUTF8NameControlAndBidiCharactersDropped(t *testing.T) {
	response, _ := bytesOf([]byte("x"), func(r *HttpResponse) { r.Download = "Grüße \"1\"\r\n\u202etxt.exe" })
	server := answering(t, response)
	got := mustSend(t, server.port, "GET", "/r", sendOptions{})
	want := `attachment; filename="Gr__e _1_txt.exe"; filename*=UTF-8''Gr%C3%BC%C3%9Fe%20%221%22txt.exe`
	if got.header("content-disposition") != want {
		t.Fatalf("content-disposition = %q, want %q", got.header("content-disposition"), want)
	}
}

func TestBytesAreAnAttachmentUnlessInlineAnInlineOneGetsASandboxCSP(t *testing.T) {
	attachedResponse, _ := bytesOf([]byte("x"), nil)
	attached := answering(t, attachedResponse)
	if got := mustSend(t, attached.port, "GET", "/r", sendOptions{}).header("content-disposition"); got != "attachment" {
		t.Fatalf("content-disposition = %q", got)
	}
	attached.close()
	shownResponse, _ := bytesOf([]byte("x"), func(r *HttpResponse) { r.Inline = true })
	shown := answering(t, shownResponse)
	got := mustSend(t, shown.port, "GET", "/r", sendOptions{})
	if got.header("content-disposition") != "inline" || !strings.HasSuffix(got.header("content-security-policy"), "; sandbox") {
		t.Fatalf("inline = %q, csp %q", got.header("content-disposition"), got.header("content-security-policy"))
	}
}

func TestBytesRefuseAForeignRequestAnotherSites403ReadingNothing(t *testing.T) {
	response, recorded := bytesOf([]byte("secret"), nil)
	server := answering(t, response)
	foreign := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"sec-fetch-site": "cross-site"}})
	if foreign.status != 403 || len(recorded.all()) != 0 {
		t.Fatalf("foreign = %d, reads %v", foreign.status, recorded.all())
	}
	if got := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"sec-fetch-site": "same-origin"}}).status; got != 200 {
		t.Fatalf("same-origin = %d", got)
	}
}

func TestBytesWithoutASizeChunkedReadWithGrowingOffsetsUntilAnEmptyChunkNoRangesR6(t *testing.T) {
	var mu sync.Mutex
	pieces := [][]byte{[]byte("ab"), []byte("cde"), {}}
	var offsets []int64
	server := answering(t, HttpResponse{
		Kind: KindBytes, Mime: "text/plain", Ranges: true,
		Read: func(offset int64, _ int, _ host.Ctx) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			offsets = append(offsets, offset)
			piece := pieces[0]
			pieces = pieces[1:]
			return piece, nil
		},
	})
	got := mustSend(t, server.port, "GET", "/r", sendOptions{headers: map[string]string{"range": "bytes=0-1"}})
	if got.status != 200 || got.text() != "abcde" || got.header("accept-ranges") != "" {
		t.Fatalf("answer = %d %q %v", got.status, got.text(), got.headers)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(offsets) != 3 || offsets[0] != 0 || offsets[1] != 2 || offsets[2] != 5 {
		t.Fatalf("offsets = %v", offsets)
	}
}

func TestBytesWithoutASizeAreSentChunked(t *testing.T) {
	var mu sync.Mutex
	pieces := [][]byte{[]byte("ab"), {}}
	server := answering(t, HttpResponse{
		Kind: KindBytes, Mime: "text/plain",
		Read: func(int64, int, host.Ctx) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			piece := pieces[0]
			pieces = pieces[1:]
			return piece, nil
		},
	})
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(server.port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET /r HTTP/1.1\r\nHost: 127.0.0.1:" + strconv.Itoa(server.port) + "\r\nConnection: close\r\n\r\n"))
	_ = conn.SetReadDeadline(time.Now().Add(within))
	var out bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		n, err := conn.Read(buffer)
		out.Write(buffer[:n])
		if err != nil {
			break
		}
	}
	if !regexp.MustCompile(`(?i)\r\ntransfer-encoding: chunked\r\n`).Match(out.Bytes()) {
		t.Fatalf("response = %q", out.String())
	}
}

func TestBytesDestroyTheConnectionOnAShortOrAThrowingReadReportedResponseFailed(t *testing.T) {
	server := serving(t, []plugin{{"@app/r", contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /short", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			size := int64(10)
			return HttpResponse{Kind: KindBytes, Mime: "text/plain", Size: &size, Read: func(int64, int, host.Ctx) ([]byte, error) { return []byte("abc"), nil }}, nil
		}),
		// Astra's build review: a throwing read hung the response, unreported.
		RouteFunc("GET /throws", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			size := int64(10)
			return HttpResponse{Kind: KindBytes, Mime: "text/plain", Size: &size, Read: func(int64, int, host.Ctx) ([]byte, error) { return nil, errors.New("read failed") }}, nil
		}),
	}})}}, servedOptions{})
	for _, path := range []string{"/short", "/throws"} {
		if _, err := send(t, server.port, "GET", path, sendOptions{}); err == nil {
			t.Fatalf("%s: the request succeeded", path)
		}
	}
	eventually(t, "both failures reported", func() bool { return server.errors.len() == 2 })
	errs := server.errors.all()
	if errs[0].Kind != ErrResponseFailed || errs[0].Pattern != "GET /short" {
		t.Fatalf("errors[0] = %+v", errs[0])
	}
	if errs[1].Kind != ErrResponseFailed || errs[1].Pattern != "GET /throws" || errs[1].Error == nil || errs[1].Error.Error() != "read failed" {
		t.Fatalf("errors[1] = %+v", errs[1])
	}
}

func TestBytesStopReadingWhenTheClientDisconnectsTheReadsContextIsAborted(t *testing.T) {
	var mu sync.Mutex
	count := 0
	var scope context.Context
	size := int64(1024 * 1024 * 1024)
	server := answering(t, HttpResponse{
		Kind: KindBytes, Mime: "application/octet-stream", Size: &size,
		Read: func(_ int64, length int, ctx host.Ctx) ([]byte, error) {
			mu.Lock()
			count++
			scope = ctx
			mu.Unlock()
			return make([]byte, length), nil
		},
	})
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(server.port))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("GET /r HTTP/1.1\r\nHost: 127.0.0.1:" + strconv.Itoa(server.port) + "\r\n\r\n"))
	_ = conn.SetReadDeadline(time.Now().Add(within))
	if _, err := conn.Read(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	after := count
	mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if scope.Err() == nil {
		t.Fatal("the read's context is not aborted")
	}
	if count != after || after >= 1000 {
		t.Fatalf("reads = %d after the disconnect, %d later", after, count)
	}
}
