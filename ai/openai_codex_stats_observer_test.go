package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/internal/codexwsobserve"
)

// pi: packages/ai/src/api/openai-codex-responses.ts getOrCreateWebSocketDebugStats, recordWebSocketFailure, recordWebSocketSseFallback, resetOpenAICodexWebSocketDebugStats
//
// The provider bridge pushes a session's counters to the helper process that owns it after each change (D74). The change sites tell an observer outside the store lock; with none installed the sites
// cost what they cost before.

type codexStatsNotice struct {
	session string
	stats   *OpenAICodexWebSocketDebugStats
	locked  bool
}

// observeCodexStats records every notice with the store's state at that moment and whether the store lock was held by the caller (a held lock makes TryLock fail).
func observeCodexStats(t *testing.T) *[]codexStatsNotice {
	t.Helper()
	var mu sync.Mutex
	var notices []codexStatsNotice
	restore := codexwsobserve.Install(func(session string) {
		held := !codexWebSocketSessions.mu.TryLock()
		if !held {
			codexWebSocketSessions.mu.Unlock()
		}
		stats := GetOpenAICodexWebSocketDebugStats(session)
		mu.Lock()
		notices = append(notices, codexStatsNotice{session: session, stats: stats, locked: held})
		mu.Unlock()
	})
	t.Cleanup(restore)
	return &notices
}

func TestCodexStatsObserverIsToldAfterEachChangeOutsideTheStoreLock(t *testing.T) {
	ResetOpenAICodexWebSocketDebugStats()
	defer ResetOpenAICodexWebSocketDebugStats()
	notices := observeCodexStats(t)

	recordCodexWebSocketFailure("obs-a", errors.New("boom"))
	recordCodexWebSocketSSEFallback("obs-a")
	ResetOpenAICodexWebSocketDebugStats("obs-a")
	ResetOpenAICodexWebSocketDebugStats()
	recordCodexWebSocketFailure("", errors.New("no session"))
	recordCodexWebSocketSSEFallback("")

	if len(*notices) != 4 {
		t.Fatalf("%d notices, want 4 (failure, fallback, reset of one session, reset of all; no session id is not recorded): %+v", len(*notices), *notices)
	}
	failure, fallback, resetOne, resetAll := (*notices)[0], (*notices)[1], (*notices)[2], (*notices)[3]
	if failure.session != "obs-a" || failure.stats == nil || failure.stats.WebSocketFailures != 1 || failure.stats.LastWebSocketError != "boom" {
		t.Errorf("failure notice %+v: the observer must see the change already made", failure)
	}
	if fallback.session != "obs-a" || fallback.stats == nil || fallback.stats.SSEFallbacks != 1 || !fallback.stats.WebSocketFallbackActive {
		t.Errorf("fallback notice %+v", fallback)
	}
	if resetOne.session != "obs-a" || resetOne.stats != nil {
		t.Errorf("reset notice %+v: the session's counters are gone", resetOne)
	}
	if resetAll.session != "" || resetAll.stats != nil {
		t.Errorf("reset-all notice %+v: the empty session id means every session", resetAll)
	}
	for _, notice := range *notices {
		if notice.locked {
			t.Errorf("notice for %q was delivered with the store lock held", notice.session)
		}
	}
}

// The request that is written to a connection counts itself before the first event can be read: the notice for it comes from the request path, with the counters of that request.
func TestCodexStatsObserverIsToldWhenARequestIsCounted(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = reviewWriteCompleted(conn, "obs")
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()
	defer ResetOpenAICodexWebSocketDebugStats()
	notices := observeCodexStats(t)

	stream, err := reviewCodexProvider(t, server.URL, "acct_obs").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportAuto, SessionID: "obs-req", TimeoutMs: new(1000)})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("stop %q: %s", result.StopReason, result.ErrorMessage)
	}
	var counted *codexStatsNotice
	for i := range *notices {
		if (*notices)[i].session == "obs-req" && (*notices)[i].stats != nil && (*notices)[i].stats.Requests == 1 {
			counted = &(*notices)[i]
		}
	}
	if counted == nil {
		t.Fatalf("no notice carried the counted request: %+v", *notices)
	}
	if counted.locked || counted.stats.ConnectionsCreated != 1 || counted.stats.LastInputItems != 1 || counted.stats.FullContextRequests != 1 {
		t.Errorf("request notice %+v", counted)
	}
}

// With no observer installed a change site allocates nothing: the hook is one atomic load.
func TestCodexStatsChangeSitesAllocateNothingWithNoObserver(t *testing.T) {
	ResetOpenAICodexWebSocketDebugStats()
	defer ResetOpenAICodexWebSocketDebugStats()
	recordCodexWebSocketSSEFallback("alloc")
	if allocs := testing.AllocsPerRun(200, func() { recordCodexWebSocketSSEFallback("alloc") }); allocs != 0 {
		t.Errorf("recordCodexWebSocketSSEFallback allocates %v times per call with no observer, want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(200, func() { ResetOpenAICodexWebSocketDebugStats("alloc-none") }); allocs != 0 {
		t.Errorf("ResetOpenAICodexWebSocketDebugStats allocates %v times per call with no observer, want 0", allocs)
	}
}
