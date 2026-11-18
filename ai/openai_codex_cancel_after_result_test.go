package ai

// pi: packages/ai/src/api/openai-codex-responses.ts (websocketSessionCache, releaseWebSocket after the terminal event; the abort listener is removed when the request ends)

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A caller that cancels its context once the stream has ended (the ordinary `defer cancel()` of a request) must not close the connection the provider keeps for the session: the next stream of the
// session reuses it, as Pi's does (Pi removes its abort listener when the request ends, and the cache entry stays).
func TestCodexCancelAfterTheTerminalEventKeepsTheSessionsConnection(t *testing.T) {
	var upgrades atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		upgrades.Add(1)
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if reviewWriteCompleted(conn, "cancel") != nil {
				return
			}
		}
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()

	provider := reviewCodexProvider(t, server.URL, "acct_cancel")
	const streams = 60
	for i := range streams {
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := provider.Stream(ctx, reviewCodexCtx("hi"), StreamOptions{Transport: TransportAuto, SessionID: "cancel-after", TimeoutMs: new(2000)})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		cancel()
		if result.StopReason != StopReasonStop {
			t.Fatalf("stream %d: stop %q: %s", i, result.StopReason, result.ErrorMessage)
		}
	}
	if got := upgrades.Load(); got != 1 {
		t.Fatalf("%d connections for %d sequential streams of one session, want 1: a cancel after the terminal event closed the cached connection", got, streams)
	}
}

// Pi's rule for an abort that arrives while the terminal event is still waiting to be consumed: parseWebSocket checks the signal at the top of every iteration, before it yields a queued event and the terminal one
// included (openai-codex-responses.ts:1404-1406), so the request ends "Request was aborted" (stop reason aborted); processWebSocketStream then keeps no continuation and releases with keep false (:1584-1586, :1606-1609),
// which closes the socket and removes the cache entry, and the session's next request finds no entry and dials a new connection. It is never handed a closed socket: an entry is reused only while its readyState is OPEN
// (:1066-1070, :1210). Go's cancel can land between the feeder's read of the terminal frame and the point where it stops the abort close, with the close already sent; the seam cancels exactly there and waits until
// the server has received that close, so the order does not depend on timing. The Go provider ends that stream aborted too (processResponsesStream's loop-top check of the context, openai_responses.go), so keep is false and
// the closed connection is not cached: this test pins that order.
func TestCodexCancelBeforeTheTerminalFrameIsHandedOverDropsTheConnection(t *testing.T) {
	const session = "abort-before-terminal"
	var upgrades atomic.Int32
	abortClosed := make(chan struct{})
	var closedOnce sync.Once
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		upgrades.Add(1)
		defer func() { _ = conn.Close() }()
		reply := conn.CloseHandler()
		conn.SetCloseHandler(func(code int, text string) error {
			if text == "aborted" {
				closedOnce.Do(func() { close(abortClosed) })
			}
			return reply(code, text)
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if reviewWriteCompleted(conn, "abort") != nil {
				return
			}
		}
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var cancelOnce sync.Once
	seam := func(id string) {
		if id != session {
			return
		}
		cancelOnce.Do(func() {
			cancel()
			select {
			case <-abortClosed:
			case <-time.After(testbudget.Wait(t)):
				t.Error("the abort close did not reach the server")
			}
		})
	}
	codexWebSocketTerminalSeam.Store(&seam)
	defer codexWebSocketTerminalSeam.Store(nil)

	provider := reviewCodexProvider(t, server.URL, "acct_abort")
	options := StreamOptions{Transport: TransportAuto, SessionID: session, TimeoutMs: new(2000)}
	first, err := provider.Stream(ctx, reviewCodexCtx("hi"), options)
	if err != nil {
		t.Fatal(err)
	}
	if result := first.Result(); result.StopReason != StopReasonAborted {
		t.Errorf("a cancel before the terminal frame was handed over ended the stream with stop %q (%s), want aborted as Pi's loop-top signal check throws", result.StopReason, result.ErrorMessage)
	}
	acquired, err := acquireCodexWebSocket(t.Context(), server.URL, http.Header{}, session, "acct_abort", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if acquired.reused {
		acquired.release(false)
		t.Fatal("the session's next request was handed the connection the abort had closed: a closed connection was kept for the session")
	}
	acquired.release(true)
	second, err := provider.Stream(t.Context(), reviewCodexCtx("again"), options)
	if err != nil {
		t.Fatal(err)
	}
	if result := second.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("the next request of the session ended with stop %q: %s", result.StopReason, result.ErrorMessage)
	}
	if got := upgrades.Load(); got != 2 {
		t.Fatalf("%d connections, want 2: the aborted one is dropped, one dial serves the next request and is kept", got)
	}
}
