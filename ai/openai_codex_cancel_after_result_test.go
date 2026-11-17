package ai

// pi: packages/ai/src/api/openai-codex-responses.ts (websocketSessionCache, releaseWebSocket after the terminal event; the abort listener is removed when the request ends)

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
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
