package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// gatedRefreshAuth is the authProvider of Pi's "does not retry requests that were cancelled while authorization was
// recovering": every onUnauthorized joins one shared refresh, held until the test releases it, that sets the token to
// "new"; the third waiter signals allWaiting.
type gatedRefreshAuth struct {
	mu         sync.Mutex
	token      string
	refresh    chan struct{}
	release    chan struct{}
	refreshes  int
	waiting    int
	allWaiting chan struct{}
}

func (a *gatedRefreshAuth) Token(context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token, nil
}

func (a *gatedRefreshAuth) OnUnauthorized(context.Context, mcp.UnauthorizedContext) error {
	a.mu.Lock()
	if a.refresh == nil {
		a.refreshes++
		refresh := make(chan struct{})
		a.refresh = refresh
		go func() {
			<-a.release
			a.mu.Lock()
			a.token = "new"
			a.mu.Unlock()
			close(refresh)
		}()
	}
	refresh := a.refresh
	a.waiting++
	if a.waiting == 3 {
		close(a.allWaiting)
	}
	a.mu.Unlock()
	<-refresh
	return nil
}

// dispatchFetch records, when the transport calls Do (even with a request whose context already ended), the tool
// name of each tools/call it dispatches with the refreshed token; Client.Close joins every send, so the record is
// complete once Close returns, whether or not a late retry reached the server.
type dispatchFetch struct {
	mu      sync.Mutex
	retried []string
}

func (f *dispatchFetch) Do(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost && request.Header.Get("Authorization") == "Bearer new" {
		data, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(data))
		var message struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.Unmarshal(data, &message) == nil && message.Method == "tools/call" {
			f.mu.Lock()
			f.retried = append(f.retried, message.Params.Name)
			f.mu.Unlock()
		}
	}
	return http.DefaultClient.Do(request)
}

// Pi streamable-http.test.ts:345-403 (#10598): a request cancelled or timed out while waiting for a shared
// authorization refresh is not retried. The contract is Pi transport.ts:9-16 (McpTransportSendOptions.signal: a
// transport must not dispatch the message once it is aborted), streamable-http.ts:295-317 (authorizedFetch checks the
// signal after getting the token, before each fetch) and client.ts:440-442,582 (the client aborts the send signal
// once the request settles).
func TestStreamableHTTPTransportDoesNotRetryRequestsThatWereCancelledWhileAuthorizationWasRecovering(t *testing.T) {
	var executedMu sync.Mutex
	var executed []string
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method != http.MethodPost {
			plainProtocol(w, r, requests)
			return
		}
		message := readJSONBody(r)
		if message["method"] == "tools/call" && r.Header.Get("Authorization") == "Bearer old" {
			w.Header().Set("Www-Authenticate", "Bearer")
			w.WriteHeader(401)
			return
		}
		if message["method"] == "tools/call" {
			params, _ := message["params"].(map[string]any)
			name, _ := params["name"].(string)
			executedMu.Lock()
			executed = append(executed, name)
			executedMu.Unlock()
		}
		protocolHandler(w, r, requests, message)
	})
	auth := &gatedRefreshAuth{token: "old", release: make(chan struct{}), allWaiting: make(chan struct{})}
	fetch := &dispatchFetch{}
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, Fetch: fetch, OpenGetStream: new(false), AuthProvider: auth})); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result *mcp.CallToolResult
		err    error
	}
	call := func(ctx context.Context, name string, options mcp.RequestOptions) <-chan outcome {
		done := make(chan outcome, 1)
		go func() {
			result, err := client.CallTool(ctx, name, map[string]any{}, options)
			done <- outcome{result, err}
		}()
		return done
	}
	abortCtx, abort := context.WithCancel(t.Context())
	defer abort()
	aborted := call(abortCtx, "aborted", mcp.RequestOptions{})
	timedOut := call(t.Context(), "timed-out", mcp.RequestOptions{TimeoutMs: 200})
	kept := call(t.Context(), "kept", mcp.RequestOptions{})
	<-auth.allWaiting
	abort()
	if got := <-aborted; !errors.As(got.err, new(*mcp.McpAbortError)) {
		t.Fatalf("aborted call error = %v, want McpAbortError", got.err)
	}
	if got := <-timedOut; !errors.As(got.err, new(*mcp.McpTimeoutError)) {
		t.Fatalf("timed-out call error = %v, want McpTimeoutError", got.err)
	}
	close(auth.release)
	got := <-kept
	if got.err != nil {
		t.Fatal(got.err)
	}
	jsonEqual(t, got.result, `{"content":[{"type":"text","text":"hello"}]}`)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	// Close joins the client's send goroutines, so every retry the transport would dispatch has called Do by now.
	fetch.mu.Lock()
	retried := slices.Clone(fetch.retried)
	fetch.mu.Unlock()
	if !slices.Equal(retried, []string{"kept"}) {
		t.Fatalf("tools/call retried = %v, want [kept]", retried)
	}
	executedMu.Lock()
	defer executedMu.Unlock()
	if !slices.Equal(executed, []string{"kept"}) {
		t.Fatalf("executed = %v, want [kept]", executed)
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if auth.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", auth.refreshes)
	}
}

// heldTokenAuth holds the transport in Token until release closes; entered closes once the first Token call waits.
type heldTokenAuth struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (a *heldTokenAuth) Token(context.Context) (string, error) {
	a.once.Do(func() { close(a.entered) })
	<-a.release
	return "token", nil
}

// Pi transport.ts:9-16 (McpTransportSendOptions.signal, Go's Send ctx) and streamable-http.ts:231-240,295-303: send passes its
// signal to authorizedFetch, which checks it after getting the token and before dispatch. A Send whose ctx ends while the
// transport waits for the token fails with McpAbortError and never reaches the server; a Send with a live ctx is dispatched.
// mutation-checked: Send handing authorizedFetch a nil signal instead of ctx fails it.
func TestStreamableHTTPTransportSendDoesNotDispatchOnceItsContextEnds(t *testing.T) {
	var postsMu sync.Mutex
	var posts []string
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodPost {
			message := readJSONBody(r)
			method, _ := message["method"].(string)
			postsMu.Lock()
			posts = append(posts, method)
			postsMu.Unlock()
			protocolHandler(w, r, requests, message)
			return
		}
		plainProtocol(w, r, requests)
	})
	auth := &heldTokenAuth{entered: make(chan struct{}), release: make(chan struct{})}
	transport := newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false), AuthProvider: auth})
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	sent := make(chan error, 1)
	go func() { sent <- transport.Send(ctx, mcp.NewNotification("notifications/cancelled-send", nil)) }()
	<-auth.entered
	cancel()
	close(auth.release)
	if err := <-sent; !errors.As(err, new(*mcp.McpAbortError)) {
		t.Fatalf("Send after its ctx ended = %v, want McpAbortError", err)
	}
	if err := transport.Send(t.Context(), mcp.NewNotification("notifications/live-send", nil)); err != nil {
		t.Fatal(err)
	}
	postsMu.Lock()
	defer postsMu.Unlock()
	if !slices.Equal(posts, []string{"notifications/live-send"}) {
		t.Fatalf("dispatched %v, want only the live send", posts)
	}
}
