package cli

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// pi: packages/ai/src/api/openai-codex-responses.ts closeOpenAICodexWebSocketSessions / resetOpenAICodexWebSocketDebugStats / getOpenAICodexWebSocketDebugStats
//
// The provider bridge serves the three Codex WebSocket helpers of a helper process that imports pi-ai's builtin APIs (D74): {type:"codexWebSocket", op:"close"|"reset", sessionId?} lines act on the
// Go cache of this bridge process in the order they arrive, and the bridge pushes {type:"codexStats", sessionId?, stats?} lines, before the result line of the stream that changed the counters.

// codexWireServer is one local Codex endpoint: a WebSocket upgrade is a connection (counted), each message answers with a completed response, mode "drop" ends the connection after partial output, and a POST
// is the SSE transport.
type codexWireServer struct {
	*httptest.Server
	upgrades, messages, posts atomic.Int32
	mu                        sync.Mutex
	mode                      string
}

func (s *codexWireServer) setMode(mode string) {
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
}

func (s *codexWireServer) currentMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func newCodexWireServer(t *testing.T) *codexWireServer {
	t.Helper()
	wire := &codexWireServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	wire.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			wire.posts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_sse\",\"status\":\"completed\"}}\n\n")
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		wire.upgrades.Add(1)
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			wire.messages.Add(1)
			if wire.currentMode() == "drop" {
				_ = conn.WriteJSON(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_x", "status": "in_progress"}})
				_ = conn.WriteJSON(map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": "partial"})
				return
			}
			for _, event := range []map[string]any{
				{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_ok", "status": "in_progress"}},
				{"type": "response.output_text.delta", "output_index": 0, "delta": "ok"},
				{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_ok", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}},
				{"type": "response.completed", "response": map[string]any{"id": "resp_ok", "status": "completed"}},
			} {
				if err := conn.WriteJSON(event); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(wire.Close)
	return wire
}

func codexBridgeLine(id, baseURL, sessionID string) string {
	claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct_bridge"}})
	line, _ := json.Marshal(map[string]any{
		"id": id, "type": "stream", "simple": false,
		"model": map[string]any{"provider": "openai-codex", "id": "gpt-5.2", "name": "Codex", "api": "openai-codex-responses", "baseUrl": baseURL, "reasoning": false,
			"input": []string{"text"}, "contextWindow": 128000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
		"request": map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi " + id, "timestamp": 0}},
			"apiKey": "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature", "sessionId": sessionID, "timeoutMs": 2000, "transport": "auto"},
	})
	return string(line) + "\n"
}

// rawBridge is a bridge whose every output line is kept as the member map the helper's JSON.parse sees.
type rawBridge struct {
	*bridgeRun
	raw chan map[string]json.RawMessage
}

func startRawBridge(t *testing.T) *rawBridge {
	t.Helper()
	ai.ResetOpenAICodexWebSocketDebugStats()
	t.Cleanup(func() { ai.CloseOpenAICodexWebSocketSessions(); ai.ResetOpenAICodexWebSocketDebugStats() })
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	run := &rawBridge{bridgeRun: &bridgeRun{stdin: inW, done: make(chan int, 1)}, raw: make(chan map[string]json.RawMessage, 256)}
	go func() {
		run.done <- runProviderBridge(inR, outW)
		_ = outW.Close()
	}()
	go func() {
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 1<<20), 64<<20)
		for scanner.Scan() {
			var members map[string]json.RawMessage
			if err := json.Unmarshal(scanner.Bytes(), &members); err != nil {
				t.Errorf("bridge output is not a JSON object: %q", scanner.Text())
			}
			run.raw <- members
		}
		close(run.raw)
	}()
	t.Cleanup(func() { _ = run.stdin.Close() })
	return run
}

func (r *rawBridge) text(members map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(members[name], &value)
	return value
}

// next is the bridge's next output line. A line the bridge never writes fails the test within the wait budget: a bare receive on raw would hang to the package's clock.
func (r *rawBridge) next(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	select {
	case members, ok := <-r.raw:
		if !ok {
			t.Fatal("bridge output ended before the next line")
		}
		return members
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("no output line from the bridge within the wait budget")
	}
	return nil
}

// stream sends one Codex stream and returns every line up to and including its result, in the order they were written.
func (r *rawBridge) stream(t *testing.T, id, baseURL, sessionID string) []map[string]json.RawMessage {
	t.Helper()
	writeBridge(t, r.stdin, codexBridgeLine(id, baseURL, sessionID))
	var seen []map[string]json.RawMessage
	timeout := time.After(testbudget.Wait(t))
	for {
		select {
		case members, ok := <-r.raw:
			if !ok {
				t.Fatalf("bridge output ended before the result of %s", id)
			}
			seen = append(seen, members)
			if r.text(members, "id") == id && (r.text(members, "type") == "result" || r.text(members, "type") == "error") {
				return seen
			}
		case <-timeout:
			t.Fatalf("no result for %s; saw %d lines", id, len(seen))
		}
	}
}

// pushes returns the codexStats lines among lines.
func (r *rawBridge) pushes(lines []map[string]json.RawMessage) []map[string]json.RawMessage {
	var pushes []map[string]json.RawMessage
	for _, members := range lines {
		if r.text(members, "type") == "codexStats" {
			pushes = append(pushes, members)
		}
	}
	return pushes
}

func (r *rawBridge) lastPush(t *testing.T, lines []map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	pushes := r.pushes(lines)
	if len(pushes) == 0 {
		t.Fatalf("no codexStats line among %d lines", len(lines))
	}
	return pushes[len(pushes)-1]
}

func (r *rawBridge) statsOf(t *testing.T, members map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var stats map[string]json.RawMessage
	if err := json.Unmarshal(members["stats"], &stats); err != nil {
		t.Fatalf("stats member %s: %v", members["stats"], err)
	}
	return stats
}

func (r *rawBridge) send(t *testing.T, line string) {
	t.Helper()
	writeBridge(t, r.stdin, line+"\n")
}

// A stream that changes a session's counters pushes the session's whole stats object, the camelCase members of Pi's OpenAICodexWebSocketDebugStats, before its result line.
func TestProviderBridgePushesCodexStatsBeforeTheResult(t *testing.T) {
	wire := newCodexWireServer(t)
	run := startRawBridge(t)
	lines := run.stream(t, "a1", wire.URL, "push-1")
	pushes := run.pushes(lines)
	if len(pushes) == 0 {
		t.Fatalf("no codexStats line before the result; saw %d lines", len(lines))
	}
	last := pushes[len(pushes)-1]
	if got := run.text(last, "sessionId"); got != "push-1" {
		t.Fatalf("push names session %q, want push-1", got)
	}
	stats := run.statsOf(t, last)
	want := map[string]string{
		"requests": "1", "connectionsCreated": "1", "connectionsReused": "0", "cachedContextRequests": "1", "storeTrueRequests": "0",
		"fullContextRequests": "1", "deltaRequests": "0", "lastInputItems": "1", "websocketFailures": "0", "sseFallbacks": "0",
	}
	for name, value := range want {
		if string(stats[name]) != value {
			t.Errorf("stats.%s = %s, want %s", name, stats[name], value)
		}
	}
	// Absent, as in Pi before a delta request, a failure or an SSE fallback: lastPreviousResponseId, lastWebSocketError and websocketFallbackActive. lastDeltaInputItems is Pi's `undefined`, which JSON.stringify omits.
	for _, name := range []string{"lastDeltaInputItems", "lastPreviousResponseId", "lastWebSocketError", "websocketFallbackActive"} {
		if _, present := stats[name]; present {
			t.Errorf("stats.%s = %s, want the member absent", name, stats[name])
		}
	}
	if len(stats) != len(want) {
		t.Errorf("stats has %d members, want the %d of Pi's fresh object: %s", len(stats), len(want), last["stats"])
	}
	// The last push precedes the result line: the replica is exact once the stream's result is read.
	resultAt, pushAt := -1, -1
	for i, members := range lines {
		switch run.text(members, "type") {
		case "result":
			resultAt = i
		case "codexStats":
			pushAt = i
		}
	}
	if pushAt > resultAt || resultAt != len(lines)-1 {
		t.Fatalf("push at line %d, result at line %d of %d", pushAt, resultAt, len(lines))
	}
}

// close names a session: the next stream of it opens a new connection (the cache entry is gone); another session's cached connection is still reused. No session id closes every cached connection.
func TestProviderBridgeCloseLineClosesTheSessionsCachedConnection(t *testing.T) {
	wire := newCodexWireServer(t)
	run := startRawBridge(t)
	run.stream(t, "c1", wire.URL, "close-a")
	run.stream(t, "c2", wire.URL, "close-b")
	run.stream(t, "c3", wire.URL, "close-a")
	if wire.upgrades.Load() != 2 {
		t.Fatalf("setup: %d connections after reusing session close-a, want 2", wire.upgrades.Load())
	}
	run.send(t, `{"id":"codex-ws-1","type":"codexWebSocket","op":"close","sessionId":"close-a"}`)
	run.stream(t, "c4", wire.URL, "close-a")
	if got := wire.upgrades.Load(); got != 3 {
		t.Fatalf("%d connections after closing close-a, want 3: its next stream opens a new one", got)
	}
	run.stream(t, "c5", wire.URL, "close-b")
	if got := wire.upgrades.Load(); got != 3 {
		t.Fatalf("%d connections: closing close-a must leave close-b's cached connection", got)
	}
	run.send(t, `{"id":"codex-ws-2","type":"codexWebSocket","op":"close"}`)
	run.stream(t, "c6", wire.URL, "close-a")
	run.stream(t, "c7", wire.URL, "close-b")
	if got := wire.upgrades.Load(); got != 5 {
		t.Fatalf("%d connections after close with no id, want 5: every session reconnects", got)
	}
}

// reset clears the counters and the session's SSE-fallback flag: a session that fell back to SSE after a WebSocket failure tries the WebSocket again, and the bridge pushes the cleared state
// (a codexStats line with no stats, and no sessionId when every session was reset).
func TestProviderBridgeResetLineClearsCountersAndTheSSEFallbackFlag(t *testing.T) {
	wire := newCodexWireServer(t)
	run := startRawBridge(t)
	wire.setMode("drop")
	run.stream(t, "r1", wire.URL, "reset-a")
	wire.setMode("ok")
	lines := run.stream(t, "r2", wire.URL, "reset-a")
	if wire.posts.Load() != 1 {
		t.Fatalf("setup: %d SSE requests, want the fallen-back session's second stream to use SSE", wire.posts.Load())
	}
	stats := run.statsOf(t, run.lastPush(t, lines))
	if string(stats["websocketFailures"]) != "1" || string(stats["sseFallbacks"]) != "1" || string(stats["websocketFallbackActive"]) != "true" {
		t.Fatalf("fallback stats %s", stats)
	}
	upgrades := wire.upgrades.Load()
	run.send(t, `{"id":"codex-ws-1","type":"codexWebSocket","op":"reset","sessionId":"reset-a"}`)
	cleared := run.next(t)
	if run.text(cleared, "type") != "codexStats" || run.text(cleared, "sessionId") != "reset-a" || len(cleared["stats"]) != 0 {
		t.Fatalf("reset of one session pushed %v", cleared)
	}
	lines = run.stream(t, "r3", wire.URL, "reset-a")
	if wire.upgrades.Load() != upgrades+1 {
		t.Fatalf("after reset the session must try the WebSocket again: %d connections, want %d", wire.upgrades.Load(), upgrades+1)
	}
	stats = run.statsOf(t, run.lastPush(t, lines))
	if string(stats["requests"]) != "1" || string(stats["sseFallbacks"]) != "0" {
		t.Fatalf("stats after reset %s, want counting from zero", stats)
	}
	if _, present := stats["websocketFallbackActive"]; present {
		t.Fatalf("websocketFallbackActive survived the reset: %s", stats)
	}
	run.send(t, `{"id":"codex-ws-2","type":"codexWebSocket","op":"reset"}`)
	cleared = run.next(t)
	if run.text(cleared, "type") != "codexStats" || len(cleared["sessionId"]) != 0 || len(cleared["stats"]) != 0 {
		t.Fatalf("reset of every session pushed %v", cleared)
	}
	if ai.GetOpenAICodexWebSocketDebugStats("reset-a") != nil {
		t.Fatal("the Go store still holds the session's counters after reset")
	}
}

// A close or reset line is one-way: no reply of its own. A line with no id or an unknown op is an error on the same id, as every request line of the bridge.
func TestProviderBridgeCodexWebSocketLineShape(t *testing.T) {
	run := startRawBridge(t)
	run.send(t, `{"id":"codex-ws-1","type":"codexWebSocket","op":"close","sessionId":"none"}`)
	run.send(t, `{"id":"codex-ws-2","type":"codexWebSocket","op":"wobble"}`)
	bad := run.next(t)
	if run.text(bad, "id") != "codex-ws-2" || run.text(bad, "type") != "error" || !strings.Contains(run.text(bad, "error"), "unknown codexWebSocket op") {
		t.Fatalf("got %v: the close line must be silent and the unknown op an error on its id", bad)
	}
	run.send(t, `{"type":"codexWebSocket","op":"close"}`)
	if noID := run.next(t); !strings.Contains(run.text(noID, "error"), "no id") {
		t.Fatalf("got %v", noID)
	}
}

// Closing a session that has no connection is Pi's no-op: nothing is pushed.
func TestProviderBridgeCloseLineOfAnUnknownSessionPushesNothing(t *testing.T) {
	run := startRawBridge(t)
	run.send(t, `{"id":"codex-ws-1","type":"codexWebSocket","op":"close","sessionId":"never-used"}`)
	run.send(t, `{"id":"w","type":"wobble"}`)
	if got := run.next(t); run.text(got, "id") != "w" {
		t.Fatalf("first line after the close is %v, want the answer to the next request", got)
	}
}
