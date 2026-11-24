package ai

// pi: packages/ai/src/api/openai-codex-responses.ts:313 (`const transport = options?.transport || "auto"`, which only chooses the WebSocket path) and :1539 (`useCachedContext = options?.transport === "websocket-cached" || options?.transport === "auto"`, read from the raw option)

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// Pi decides the cached context from the option as the caller passed it. An unset transport, "websocket", "sse" and a value Pi does not know are none of the two names, so the request sends the full context.
func TestCodexUsesCachedContextReadsTheRawTransport(t *testing.T) {
	for _, tc := range []struct {
		transport Transport
		want      bool
	}{
		{"", false},
		{TransportAuto, true},
		{TransportWebSocketCached, true},
		{TransportWebSocket, false},
		{TransportSSE, false},
		{"bogus", false},
	} {
		if got := codexUsesCachedContext(tc.transport); got != tc.want {
			t.Errorf("codexUsesCachedContext(%q) = %v, want %v", tc.transport, got, tc.want)
		}
	}
}

// Two turns of one session against one local Codex WebSocket server, per transport value: the second request is the delta (one input item after previous_response_id) where Pi's useCachedContext is true and the full
// context (the first request's item, the answer, the new message) where it is false, and the session's stats count the same.
func TestCodexSecondRequestOfASessionFollowsTheRawTransport(t *testing.T) {
	var mu sync.Mutex
	var frames []string
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			frames = append(frames, string(data))
			index := strconv.Itoa(len(frames))
			mu.Unlock()
			if reviewWriteCompleted(conn, index) != nil {
				return
			}
		}
	}))
	defer server.Close()
	provider := reviewCodexProvider(t, server.URL, "acct_raw_transport")
	for _, tc := range []struct {
		name      string
		transport Transport
		cached    bool
	}{
		{"unset", "", false},
		{"auto", TransportAuto, true},
		{"websocket-cached", TransportWebSocketCached, true},
		{"websocket", TransportWebSocket, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "raw-transport-" + tc.name
			ResetOpenAICodexWebSocketDebugStats(session)
			defer CloseOpenAICodexWebSocketSessions(session)
			defer ResetOpenAICodexWebSocketDebugStats(session)
			mu.Lock()
			frames = nil
			mu.Unlock()
			options := StreamOptions{SessionID: session, Transport: tc.transport, TimeoutMs: new(2000)}
			one := UserMessage{Content: UserText("one"), Timestamp: 1}
			firstStream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "be brief", Messages: []Message{one}}), options)
			if err != nil {
				t.Fatal(err)
			}
			first := firstStream.Result()
			if first.StopReason != StopReasonStop {
				t.Fatalf("first: %s", first.ErrorMessage)
			}
			secondStream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "be brief", Messages: []Message{one, *first, UserMessage{Content: UserText("two"), Timestamp: 2}}}), options)
			if err != nil {
				t.Fatal(err)
			}
			if second := secondStream.Result(); second.StopReason != StopReasonStop {
				t.Fatalf("second: %s", second.ErrorMessage)
			}
			mu.Lock()
			sent := slices.Clone(frames)
			mu.Unlock()
			if len(sent) != 2 {
				t.Fatalf("%d requests, want 2", len(sent))
			}
			var second struct {
				PreviousResponseID string            `json:"previous_response_id"`
				Input              []json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal([]byte(sent[1]), &second); err != nil {
				t.Fatal(err)
			}
			stats := GetOpenAICodexWebSocketDebugStats(session)
			if tc.cached {
				if second.PreviousResponseID == "" || len(second.Input) != 1 || stats.CachedContextRequests != 2 || stats.DeltaRequests != 1 || stats.FullContextRequests != 1 {
					t.Errorf("second request: previous_response_id %q, %d input items; stats %+v, want the delta (one item after a previous_response_id), 2 cached-context requests, 1 delta, 1 full", second.PreviousResponseID, len(second.Input), stats)
				}
				return
			}
			// packages/ai/src/api/openai-codex-responses.ts:555 (Pi edfe6b2ef): full context includes the leading developer item (4 items).
			if second.PreviousResponseID != "" || len(second.Input) != 4 || stats.CachedContextRequests != 0 || stats.DeltaRequests != 0 || stats.FullContextRequests != 2 {
				t.Errorf("second request: previous_response_id %q, %d input items; stats %+v, want the full context (4 items, no previous_response_id), no cached-context request, 2 full", second.PreviousResponseID, len(second.Input), stats)
			}
		})
	}
}

// The transport value is read in three places of the package: the path choice (`transport != "sse"`, Pi :320), the transport a failure diagnostic names (Pi's defaulted `transport`, :378) and the cached-context decision.
// Only the last one distinguishes an unset value from "auto". This pins that, for an OAuth provider (Codex), an API-key provider (OpenAI Responses) and an OpenAI-compatible provider on a custom base URL: the first
// request is the same bytes and the same header names whatever the value is; the one difference is Codex's cached-context count.
func TestTheTransportValueChangesOnlyTheCodexCachedContextDecision(t *testing.T) {
	type wire struct {
		headers []string
		body    string
	}
	values := []Transport{"", TransportAuto, TransportSSE, TransportWebSocket, TransportWebSocketCached}
	httpFirstRequest := func(t *testing.T, build func(url string) Provider, reply string, transport Transport) wire {
		t.Helper()
		var once sync.Once
		var got wire
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() {
				for name := range r.Header {
					got.headers = append(got.headers, name)
				}
				slices.Sort(got.headers)
				body, _ := io.ReadAll(r.Body)
				got.body = string(body)
			})
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(reply))
		}))
		defer server.Close()
		stream, err := build(server.URL).Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "be brief", Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}), StreamOptions{Transport: transport, TimeoutMs: new(2000)})
		if err != nil {
			t.Fatal(err)
		}
		stream.Result()
		return got
	}
	for _, shape := range []struct {
		name  string
		build func(url string) Provider
		reply string
	}{
		{"api-key provider (OpenAI Responses)", func(url string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "key", Model: "gpt-5.2", ProviderID: "openai"})
		}, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"},
		{"OpenAI-compatible provider on a custom base URL", func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "key", Model: "wire-model", ProviderID: "custom-compat"})
		}, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(shape.name, func(t *testing.T) {
			want := httpFirstRequest(t, shape.build, shape.reply, values[0])
			if want.body == "" {
				t.Fatal("no request reached the server")
			}
			for _, transport := range values[1:] {
				if got := httpFirstRequest(t, shape.build, shape.reply, transport); got.body != want.body || !slices.Equal(got.headers, want.headers) {
					t.Errorf("transport %q changed the request:\n unset: %v %s\n got  : %v %s", transport, want.headers, want.body, got.headers, got.body)
				}
			}
		})
	}
	t.Run("OAuth provider (Codex WebSocket)", func(t *testing.T) {
		var mu sync.Mutex
		var first []string
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			first = append(first, string(data))
			mu.Unlock()
			_ = reviewWriteCompleted(conn, "shape")
		}))
		defer server.Close()
		provider := reviewCodexProvider(t, server.URL, "acct_shape")
		var bodies []string
		// One session id for every value: it is the request's prompt_cache_key, so a different id would differ in the first request for a reason that is not the transport.
		const session = "shape"
		for _, tc := range []struct {
			transport Transport
			cached    int
		}{{"", 0}, {TransportAuto, 1}, {TransportWebSocket, 0}, {TransportWebSocketCached, 1}} {
			ResetOpenAICodexWebSocketDebugStats(session)
			stream, err := provider.Stream(t.Context(), reviewCodexCtx("hi"), StreamOptions{SessionID: session, Transport: tc.transport, TimeoutMs: new(2000)})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatalf("transport %q: %s", tc.transport, result.ErrorMessage)
			}
			mu.Lock()
			bodies = append(bodies, first[len(first)-1])
			mu.Unlock()
			stats := GetOpenAICodexWebSocketDebugStats(session)
			if stats.Requests != 1 || stats.CachedContextRequests != tc.cached {
				t.Errorf("transport %q: stats %+v, want 1 request and %d cached-context requests", tc.transport, stats, tc.cached)
			}
			CloseOpenAICodexWebSocketSessions(session)
		}
		for i, body := range bodies[1:] {
			if body != bodies[0] {
				t.Errorf("transport %d changed the first request:\n unset: %s\n got  : %s", i+1, bodies[0], body)
			}
		}
	})
}
