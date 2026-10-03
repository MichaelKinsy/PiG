package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/streamable-http.test.ts.

type recordedRequest struct {
	method  string
	headers http.Header
	message map[string]any
}

type requestLog struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (l *requestLog) add(r recordedRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, r)
}

func (l *requestLog) all() []recordedRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]recordedRequest(nil), l.requests...)
}

type httpHandler func(w http.ResponseWriter, r *http.Request, requests *requestLog)

func startServer(t *testing.T, handler httpHandler) (string, *requestLog) {
	t.Helper()
	requests := &requestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, requests)
	}))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return server.URL + "/mcp", requests
}

func readJSONBody(r *http.Request) map[string]any {
	data, _ := io.ReadAll(r.Body)
	var message map[string]any
	_ = json.Unmarshal(data, &message)
	return message
}

func writeJSON(w http.ResponseWriter, status int, headers map[string]string, value any) {
	w.Header().Set("Content-Type", "application/json")
	for name, header := range headers {
		w.Header().Set(name, header)
	}
	w.WriteHeader(status)
	data, _ := json.Marshal(value)
	_, _ = w.Write(data)
}

// protocolHandler is the fixture server of streamable-http.test.ts.
func protocolHandler(w http.ResponseWriter, r *http.Request, requests *requestLog, body map[string]any) {
	if r.Method == http.MethodGet {
		requests.add(recordedRequest{method: "GET", headers: r.Header.Clone()})
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodDelete {
		requests.add(recordedRequest{method: "DELETE", headers: r.Header.Clone()})
		w.WriteHeader(http.StatusOK)
		return
	}
	message := body
	if message == nil {
		message = readJSONBody(r)
	}
	requests.add(recordedRequest{method: r.Method, headers: r.Header.Clone(), message: message})
	id, hasID := message["id"]
	if !hasID {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch message["method"] {
	case "initialize":
		writeJSON(w, 200, map[string]string{"Mcp-Session-Id": "session-1"}, map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"protocolVersion": mcp.LatestProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "http-fixture", "version": "1.0.0"},
			},
		})
	case "tools/list":
		writeJSON(w, 200, nil, map[string]any{
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}},
		})
	default:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello"}}}})
		_, _ = fmt.Fprintf(w, "id: tool-result\ndata: %s\n\n", data)
	}
}

func plainProtocol(w http.ResponseWriter, r *http.Request, requests *requestLog) {
	protocolHandler(w, r, requests, nil)
}

func newHTTPClient() *mcp.Client {
	return mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "http-test", Version: "1.0.0"}})
}

func newHTTPTransport(t *testing.T, options mcp.StreamableHTTPTransportOptions) *mcp.StreamableHTTPTransport {
	t.Helper()
	transport, err := mcp.NewStreamableHTTPTransport(options)
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

type chunkReader struct {
	chunks []string
	i      int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.i >= len(c.chunks) {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[c.i])
	c.chunks[c.i] = c.chunks[c.i][n:]
	if c.chunks[c.i] == "" {
		c.i++
	}
	return n, nil
}

func TestConsumeSSEStreamParsesChunkedCRLFEventsCommentsIDsAndMultilineData(t *testing.T) {
	stream := &chunkReader{chunks: []string{": keepalive\r\nid: 7\r\ndata: {\"one\":\r\n", "data: 1}\r\n\r\n"}}
	var events []mcp.SSEEvent
	if err := mcp.ConsumeSSEStream(stream, mcp.ConsumeSSEOptions{OnEvent: func(e mcp.SSEEvent) { events = append(events, e) }}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != (mcp.SSEEvent{ID: "7", Data: "{\"one\":\n1}"}) {
		t.Fatalf("events = %#v", events)
	}
}

type endlessDataReader struct{ sent atomic.Int64 }

func (r *endlessDataReader) Read(p []byte) (int, error) {
	// Never sends a blank line, so the event is never dispatched.
	if r.sent.Add(1)-1 > 1000 {
		return 0, io.EOF
	}
	return copy(p, "data: xxxxxxxxxxxxxxxx\n"), nil
}

func TestConsumeSSEStreamRejectsEventsWhoseDataLinesExceedTheLimitWithoutABlankLine(t *testing.T) {
	stream := &endlessDataReader{}
	err := mcp.ConsumeSSEStream(stream, mcp.ConsumeSSEOptions{MaxEventBytes: 256, OnEvent: func(mcp.SSEEvent) {}})
	if err == nil || err.Error() != "MCP SSE event exceeds 256 bytes" {
		t.Fatalf("err = %v", err)
	}
	if sent := stream.sent.Load(); sent >= 100 {
		t.Fatalf("read %d lines before failing", sent)
	}
}

func TestStreamableHTTPTransportHandlesJSONAndSSEResponsesWithSessionAndProtocolHeaders(t *testing.T) {
	url, requests := startServer(t, plainProtocol)
	transport := newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	if transport.SessionID() != "session-1" {
		t.Fatalf("session id = %q", transport.SessionID())
	}
	tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, tools, `[{"name":"echo","inputSchema":{"type":"object"}}]`)
	result, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"content":[{"type":"text","text":"hello"}]}`)
	// The GET stream opens after initialization; give it time before closing.
	waitFor(t, func() bool {
		for _, r := range requests.all() {
			if r.method == "GET" {
				return true
			}
		}
		return false
	})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	var list *recordedRequest
	sawGet, sawDelete := false, false
	for _, r := range requests.all() {
		if r.message["method"] == "tools/list" {
			list = &r
		}
		sawGet = sawGet || r.method == "GET"
		sawDelete = sawDelete || r.method == "DELETE"
	}
	if list == nil || list.headers.Get("mcp-session-id") != "session-1" || list.headers.Get("mcp-protocol-version") != mcp.LatestProtocolVersion {
		t.Fatalf("tools/list request = %#v", list)
	}
	if !sawGet || !sawDelete {
		t.Fatalf("GET %v DELETE %v", sawGet, sawDelete)
	}
}

func TestStreamableHTTPTransportClassifiesAuthenticationFailures(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, _ *requestLog) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Www-Authenticate", `Bearer resource_metadata="https://example.com/meta"`)
		w.WriteHeader(401)
		_, _ = io.WriteString(w, "login required")
	})
	client := newHTTPClient()
	_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url}))
	var authErr *mcp.McpAuthRequiredError
	if !errors.As(err, &authErr) {
		t.Fatalf("err = %v", err)
	}
	if authErr.Status != 401 || authErr.Body != "login required" || !authErr.HasWWWAuthenticate || authErr.WWWAuthenticate != `Bearer resource_metadata="https://example.com/meta"` {
		t.Fatalf("auth error = %#v", authErr)
	}
	if _, ok := errors.AsType[*mcp.McpHttpError](err); !ok {
		t.Fatal("McpAuthRequiredError is not an McpHttpError")
	}
}

func TestStreamableHTTPTransportFailsOnlyTheRequestWhoseSSEStreamBreaks(t *testing.T) {
	slowGate := make(chan struct{})
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method != http.MethodPost {
			plainProtocol(w, r, requests)
			return
		}
		message := readJSONBody(r)
		params, _ := message["params"].(map[string]any)
		switch params["name"] {
		case "broken":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, "data: not json\n\n")
			return
		case "slow":
			<-slowGate
		}
		protocolHandler(w, r, requests, message)
	})
	client := newHTTPClient()
	var mu sync.Mutex
	var errs []error
	client.OnError(func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	})
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false)})); err != nil {
		t.Fatal(err)
	}
	type callResult struct {
		result *mcp.CallToolResult
		err    error
	}
	slow := make(chan callResult, 1)
	go func() {
		result, err := client.CallTool(t.Context(), "slow", nil, mcp.RequestOptions{})
		slow <- callResult{result, err}
	}()
	if _, err := client.CallTool(t.Context(), "broken", nil, mcp.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "MCP response stream failed") {
		t.Fatalf("broken err = %v", err)
	}
	close(slowGate)
	r := <-slow
	if r.err != nil {
		t.Fatal(r.err)
	}
	jsonEqual(t, r.result, `{"content":[{"type":"text","text":"hello"}]}`)
	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}
	_ = client.Close()
}

func TestStreamableHTTPTransportOpensTheGETStreamAfterInitializationAndSendsLastEventIDOnlyWhenResuming(t *testing.T) {
	var mu sync.Mutex
	var order []string
	url, requests := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodPost {
			message := readJSONBody(r)
			mu.Lock()
			order = append(order, fmt.Sprint(message["method"]))
			mu.Unlock()
			protocolHandler(w, r, requests, message)
			return
		}
		mu.Lock()
		order = append(order, r.Method)
		mu.Unlock()
		protocolHandler(w, r, requests, nil)
	})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(t.Context(), "echo", nil, mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(t.Context(), mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(order, "GET")
	})
	_ = client.Close()
	mu.Lock()
	defer mu.Unlock()
	index := func(name string) int {
		for i, entry := range order {
			if entry == name {
				return i
			}
		}
		return -1
	}
	if index("GET") <= index("notifications/initialized") {
		t.Fatalf("order = %v", order)
	}
	for _, r := range requests.all() {
		if r.headers.Get("last-event-id") != "" {
			t.Fatalf("Last-Event-ID sent without resuming: %#v", r)
		}
	}
}

func TestStreamableHTTPTransportResumesAResponseStreamTheServerClosedBeforeAnswering(t *testing.T) {
	var mu sync.Mutex
	var resumeHeaders []string
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodGet && r.Header.Get("Last-Event-Id") != "" {
			mu.Lock()
			resumeHeaders = append(resumeHeaders, r.Header.Get("Last-Event-Id"))
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "resumed"}}}})
			_, _ = fmt.Fprintf(w, "id: 2\ndata: %s\n\n", data)
			return
		}
		if r.Method != http.MethodPost {
			plainProtocol(w, r, requests)
			return
		}
		message := readJSONBody(r)
		if message["method"] != "tools/call" {
			protocolHandler(w, r, requests, message)
			return
		}
		// Priming event (ID, no data) and a retry hint, then the server drops the stream.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "id: 1\nretry: 5\ndata:\n\n")
	})
	client := newHTTPClient()
	var mu2 sync.Mutex
	var errs []error
	client.OnError(func(err error) {
		mu2.Lock()
		errs = append(errs, err)
		mu2.Unlock()
	})
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false)})); err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(t.Context(), "echo", nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"content":[{"type":"text","text":"resumed"}]}`)
	mu.Lock()
	if len(resumeHeaders) != 1 || resumeHeaders[0] != "1" {
		t.Fatalf("resume headers = %v", resumeHeaders)
	}
	mu.Unlock()
	mu2.Lock()
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	mu2.Unlock()
	_ = client.Close()
}

func TestStreamableHTTPTransportFailsARequestWhoseResponseStreamEndsWithoutAnAnswer(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method != http.MethodPost {
			plainProtocol(w, r, requests)
			return
		}
		message := readJSONBody(r)
		if message["method"] != "tools/call" {
			protocolHandler(w, r, requests, message)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, ": nothing here\n\n")
	})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false)})); err != nil {
		t.Fatal(err)
	}
	_, err := client.CallTool(t.Context(), "echo", map[string]any{}, mcp.RequestOptions{TimeoutMs: 5_000})
	if err == nil || !strings.Contains(err.Error(), "MCP response stream failed: stream ended without a response") {
		t.Fatalf("err = %v", err)
	}
	_ = client.Close()
}

func TestStreamableHTTPTransportReconnectsTheGETStreamAfterItDrops(t *testing.T) {
	var gets atomic.Int32
	var mu sync.Mutex
	var lastEventIDs []string
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method != http.MethodGet {
			plainProtocol(w, r, requests)
			return
		}
		n := gets.Add(1)
		mu.Lock()
		lastEventIDs = append(lastEventIDs, r.Header.Get("Last-Event-Id"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
		if n == 1 {
			_, _ = fmt.Fprintf(w, "id: g1\ndata: %s\n\n", notification)
			return
		}
		_, _ = fmt.Fprintf(w, "id: g2\ndata: %s\n\n", notification)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	client := newHTTPClient()
	var changes atomic.Int32
	secondChange := make(chan struct{})
	client.OnNotification("notifications/tools/list_changed", func(json.RawMessage) {
		if changes.Add(1) == 2 {
			close(secondChange)
		}
	})
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, Reconnect: mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 1}})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondChange:
	case <-time.After(10 * time.Second):
		t.Fatal("second list_changed never arrived")
	}
	mu.Lock()
	if len(lastEventIDs) < 2 || lastEventIDs[0] != "" || lastEventIDs[1] != "g1" {
		t.Fatalf("last event ids = %q", lastEventIDs)
	}
	mu.Unlock()
	_ = client.Close()
}

func TestStreamableHTTPTransportRejectsARequestTheServerAcceptsWithoutAResponse(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method != http.MethodPost {
			plainProtocol(w, r, requests)
			return
		}
		message := readJSONBody(r)
		if message["method"] != "tools/call" {
			protocolHandler(w, r, requests, message)
			return
		}
		w.WriteHeader(202)
	})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false)})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(t.Context(), "echo", nil, mcp.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "without a response") {
		t.Fatalf("err = %v", err)
	}
	_ = client.Close()
}

func TestStreamableHTTPTransportIncludesTheResponseBodyInHTTPErrors(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, _ *requestLog) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(400)
		_, _ = io.WriteString(w, "Invalid Accept header")
	})
	client := newHTTPClient()
	_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url}))
	if err == nil || !strings.Contains(err.Error(), "MCP HTTP request failed with status 400: Invalid Accept header") {
		t.Fatalf("err = %v", err)
	}
}

type recordingAuth struct {
	mu    sync.Mutex
	token string
	seen  []string
}

func (a *recordingAuth) Token(context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token, nil
}

func (a *recordingAuth) OnUnauthorized(_ context.Context, u mcp.UnauthorizedContext) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = append(a.seen, fmt.Sprintf("%d:%s", u.Response.StatusCode, u.Token))
	if u.Response.StatusCode == 401 {
		a.token = "new"
	} else {
		a.token = "admin"
	}
	return nil
}

func TestStreamableHTTPTransportHandsUnauthorizedAndInsufficientScopeResponsesToTheAuthProviderWithTheRejectedToken(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method != http.MethodPost {
			plainProtocol(w, r, requests)
			return
		}
		message := readJSONBody(r)
		if r.Header.Get("Authorization") == "Bearer old" {
			w.Header().Set("Www-Authenticate", "Bearer")
			w.WriteHeader(401)
			return
		}
		if message["method"] == "tools/call" && r.Header.Get("Authorization") == "Bearer new" {
			w.Header().Set("Www-Authenticate", `Bearer error="insufficient_scope", scope="admin"`)
			w.WriteHeader(403)
			return
		}
		protocolHandler(w, r, requests, message)
	})
	auth := &recordingAuth{token: "old"}
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false), AuthProvider: auth})); err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(t.Context(), "echo", nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"content":[{"type":"text","text":"hello"}]}`)
	auth.mu.Lock()
	seen := strings.Join(auth.seen, ",")
	auth.mu.Unlock()
	if seen != "401:old,403:new" {
		t.Fatalf("seen = %v", seen)
	}
	_ = client.Close()
}

// recordingFetch is a McpFetch that records the method of every request it carries and sends it on with http.DefaultClient.
type recordingFetch struct {
	mu      sync.Mutex
	methods []string
}

func (f *recordingFetch) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.methods = append(f.methods, request.Method)
	f.mu.Unlock()
	return http.DefaultClient.Do(request)
}

func (f *recordingFetch) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.methods)
}

// recordingRoundTripper stands in for the default client's transport, as the case's vi.stubGlobal("fetch", ...) stands in for the global fetch.
type recordingRoundTripper struct {
	next    http.RoundTripper
	mu      sync.Mutex
	methods []string
}

func (r *recordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.methods = append(r.methods, request.Method)
	r.mu.Unlock()
	return r.next.RoundTrip(request)
}

func (r *recordingRoundTripper) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.methods)
}

// fetchAuth is the case's authProvider: onUnauthorized fetches the url through context.fetch, then sets the token.
type fetchAuth struct {
	t     *testing.T
	url   string
	want  mcp.McpFetch
	mu    sync.Mutex
	token string
}

func (a *fetchAuth) Token(context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token, nil
}

func (a *fetchAuth) OnUnauthorized(ctx context.Context, unauthorized mcp.UnauthorizedContext) error {
	if a.want != nil && unauthorized.Fetch != a.want {
		a.t.Errorf("UnauthorizedContext.Fetch = %T %p, want the injected fetch", unauthorized.Fetch, unauthorized.Fetch)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return err
	}
	response, err := unauthorized.Fetch.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	a.mu.Lock()
	a.token = "token"
	a.mu.Unlock()
	return nil
}

// Ports "calls fetch without a receiver" (0.99.2, #10188; .upstream/v0.99.2/packages/mcp/test/streamable-http.test.ts:346-388).
// The strict receiver check (`this !== undefined && this !== globalThis`) is a JavaScript mechanic: a Go McpFetch is an
// interface value whose Do always runs on its own receiver. The observable part is ported: the injected fetch carries every
// transport request, including the UnauthorizedContext.Fetch the auth provider uses, and an absent fetch falls back to the
// default client (mcp/streamable_http.go NewStreamableHTTPTransport).
func TestStreamableHTTPTransportRoutesEveryRequestThroughTheFetchInUse(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodPost && r.Header.Get("Authorization") == "" {
			_ = readJSONBody(r)
			w.Header().Set("Www-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		plainProtocol(w, r, requests)
	})
	connect := func(fetch mcp.McpFetch) {
		t.Helper()
		auth := &fetchAuth{t: t, url: url, want: fetch}
		client := newHTTPClient()
		if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, Fetch: fetch, OpenGetStream: new(false), AuthProvider: auth})); err != nil {
			t.Fatal(err)
		}
		tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
		if err != nil {
			t.Fatal(err)
		}
		jsonEqual(t, tools, `[{"name":"echo","inputSchema":{"type":"object"}}]`)
		_ = client.Close()
	}
	// initialize: 401 POST, the provider's GET through UnauthorizedContext.Fetch, the retried POST; then the
	// initialized notification, tools/list and the closing DELETE.
	want := []string{"POST", "GET", "POST", "POST", "POST", "DELETE"}

	injected := &recordingFetch{}
	connect(injected)
	if got := injected.calls(); !slices.Equal(got, want) {
		t.Fatalf("injected fetch saw %v, want %v", got, want)
	}

	original := http.DefaultClient.Transport
	next := original
	if next == nil {
		next = http.DefaultTransport
	}
	stubbed := &recordingRoundTripper{next: next}
	http.DefaultClient.Transport = stubbed
	t.Cleanup(func() { http.DefaultClient.Transport = original })
	connect(nil)
	if got := stubbed.calls(); !slices.Equal(got, want) {
		t.Fatalf("default client saw %v, want %v", got, want)
	}
}

func TestStreamableHTTPTransportClassifiesAnExpiredEstablishedSession(t *testing.T) {
	var posts atomic.Int32
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodPost && posts.Add(1)-1 >= 2 {
			_, _ = io.ReadAll(r.Body)
			w.WriteHeader(404)
			_, _ = io.WriteString(w, "gone")
			return
		}
		plainProtocol(w, r, requests)
	})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false)})); err != nil {
		t.Fatal(err)
	}
	_, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if _, ok := errors.AsType[*mcp.McpSessionExpiredError](err); !ok {
		t.Fatalf("err = %v", err)
	}
	_ = client.Close()
}
