package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type codexWSRequest struct {
	Connection int
	Body       map[string]any
}
type codexWSPeer struct {
	server   *httptest.Server
	mu       sync.Mutex
	headers  []http.Header
	requests []codexWSRequest
	closed   chan int
	fetches  int
	reply    func(int, int, map[string]any) []string
}

func newCodexWSPeer(t *testing.T, reply func(int, int, map[string]any) []string) *codexWSPeer {
	t.Helper()
	peer := &codexWSPeer{closed: make(chan int, 16), reply: reply}
	peer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			peer.mu.Lock()
			peer.fetches++
			peer.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, codexUpstreamSSE("completed", nil))
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		peer.mu.Lock()
		peer.headers = append(peer.headers, r.Header.Clone())
		connection := len(peer.headers)
		peer.mu.Unlock()
		defer func() { peer.closed <- connection }()
		for {
			var body map[string]any
			if err := conn.ReadJSON(&body); err != nil {
				return
			}
			peer.mu.Lock()
			peer.requests = append(peer.requests, codexWSRequest{connection, body})
			index := len(peer.requests)
			peer.mu.Unlock()
			for _, frame := range reply(index, connection, body) {
				if frame == "close" {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(func() {
		CloseOpenAICodexWebSocketSessions()
		ResetOpenAICodexWebSocketDebugStats()
		peer.server.Close()
	})
	return peer
}
func (peer *codexWSPeer) provider(t *testing.T, account string) *openAIResponsesProvider {
	t.Helper()
	return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, account), Model: "gpt-5.1-codex", ProviderID: "openai-codex", BaseURL: peer.server.URL}).(*openAIResponsesProvider)
}
func (peer *codexWSPeer) snapshot() ([]http.Header, []codexWSRequest, int) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return append([]http.Header(nil), peer.headers...), append([]codexWSRequest(nil), peer.requests...), peer.fetches
}
func codexWSTerminal(id string) string {
	return fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"status":"completed","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`, id)
}
func codexWSHello(id, text string) []string {
	return []string{fmt.Sprintf(`{"type":"response.created","response":{"id":%q}}`, id), `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`, fmt.Sprintf(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q}]}}`, text), codexWSTerminal(id)}
}
func codexWSResult(t *testing.T, provider Provider, transcript TranscriptContext, opts StreamOptions) (*AssistantMessage, []AssistantEventType) {
	t.Helper()
	stream, err := provider.Stream(t.Context(), transcript, opts)
	if err != nil {
		t.Fatal(err)
	}
	var types []AssistantEventType
	for event := range stream.Events(t.Context()) {
		types = append(types, event.EventType())
	}
	return stream.Result(), types
}
func TestCodexWebSocketUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1269
	t.Run("forwards auto transport from streamSimple options and uses cached websocket context", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(int, int, map[string]any) []string {
			frames := codexWSHello("", "Hello")
			frames[len(frames)-1] = `{"type":"response.completed","response":{"status":"completed","end_turn":false,"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`
			return frames
		})
		result, _ := codexWSResult(t, peer.provider(t, "acc_test"), codexUpstreamContext(), StreamOptions{Transport: TransportAuto, SessionID: "session-auto"})
		headers, bodies, fetches := peer.snapshot()
		stats := GetOpenAICodexWebSocketDebugStats("session-auto")
		if result.EndTurn == nil || *result.EndTurn || len(bodies) != 1 || len(headers) != 1 || headers[0].Get("session-id") != "session-auto" || headers[0].Get("session_id") != "" || headers[0].Get("x-client-request-id") != "session-auto" || fetches != 0 || stats == nil || stats.CachedContextRequests != 1 || stats.FullContextRequests != 1 {
			t.Fatalf("result=%#v headers=%v bodies=%v fetches=%d stats=%#v", result, headers, bodies, fetches, stats)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1386
	t.Run("scopes cached websockets to the authenticated account", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(index, _ int, _ map[string]any) []string {
			return []string{codexWSTerminal(fmt.Sprintf("resp_%d", index))}
		})
		for _, account := range []string{"account-a", "account-b", "account-a"} {
			result, _ := codexWSResult(t, peer.provider(t, account), NormalizeContext(Context{}), StreamOptions{Transport: TransportWebSocketCached, SessionID: "shared-session"})
			if result.StopReason != StopReasonStop {
				t.Fatal(result)
			}
		}
		headers, _, fetches := peer.snapshot()
		if len(headers) != 2 {
			t.Fatal(headers)
		}
		for i, account := range []string{"account-a", "account-b"} {
			if headers[i].Get("chatgpt-account-id") != account || headers[i].Get("Authorization") != "Bearer "+codexTestToken(t, account) {
				t.Fatal(headers)
			}
		}
		stats := GetOpenAICodexWebSocketDebugStats("shared-session")
		if fetches != 0 || stats == nil || stats.ConnectionsCreated != 2 || stats.ConnectionsReused != 1 {
			t.Fatalf("fetches=%d stats=%#v", fetches, stats)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1486
	t.Run("closes one-shot websockets when cacheRetention is none", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(index, _ int, _ map[string]any) []string {
			return []string{codexWSTerminal(fmt.Sprintf("resp_%d", index))}
		})
		provider := peer.provider(t, "acc_test")
		for range 2 {
			_, _ = codexWSResult(t, provider, codexUpstreamContext(), StreamOptions{Transport: TransportAuto, SessionID: "one-off-summary", CacheRetention: CacheRetentionNone})
			select {
			case <-peer.closed:
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}
		}
		headers, bodies, fetches := peer.snapshot()
		if len(headers) != 2 || len(bodies) != 2 || fetches != 0 || GetOpenAICodexWebSocketDebugStats("one-off-summary") != nil {
			t.Fatalf("headers=%v bodies=%v fetches=%d", headers, bodies, fetches)
		}
		for _, body := range bodies {
			if _, ok := body.Body["prompt_cache_key"]; ok {
				t.Fatal(body)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1667
	t.Run("reconnects once when the websocket connection limit is reached before output starts", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(_, connection int, _ map[string]any) []string {
			if connection == 1 {
				return []string{`{"type":"error","error":{"code":"websocket_connection_limit_reached"}}`}
			}
			return []string{codexWSTerminal("resp_1")}
		})
		result, _ := codexWSResult(t, peer.provider(t, "acc_test"), NormalizeContext(Context{}), StreamOptions{})
		headers, _, fetches := peer.snapshot()
		if result.StopReason != StopReasonStop || len(headers) != 2 || fetches != 0 {
			t.Fatalf("result=%#v connections=%d fetches=%d", result, len(headers), fetches)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1912
	t.Run("opens a fresh cached websocket before the backend connection age limit", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(_, connection int, _ map[string]any) []string {
			return []string{codexWSTerminal(fmt.Sprintf("resp_%d", connection))}
		})
		provider := peer.provider(t, "acc_test")
		first, _ := codexWSResult(t, provider, codexUpstreamContext(), StreamOptions{Transport: TransportWebSocketCached, SessionID: "aged-ws-session"})
		// vi.setSystemTime jumps wall-clock age without running the five-minute idle timer.
		codexWebSocketSessions.mu.Lock()
		entry := codexWebSocketSessions.sessions["aged-ws-session"]["acc_test"]
		entry.createdAt = time.Now().Add(-56 * time.Minute)
		codexWebSocketSessions.mu.Unlock()
		messages := append(codexUpstreamContext().Messages(), *first, UserMessage{Content: UserText("Now finish"), Timestamp: 2})
		_, _ = codexWSResult(t, provider, NormalizeContext(Context{Messages: messages}), StreamOptions{Transport: TransportWebSocketCached, SessionID: "aged-ws-session"})
		headers, bodies, _ := peer.snapshot()
		stats := GetOpenAICodexWebSocketDebugStats("aged-ws-session")
		if len(headers) != 2 || len(bodies) != 2 || bodies[0].Connection != 1 || bodies[1].Connection != 2 || stats == nil || stats.ConnectionsCreated != 2 || stats.ConnectionsReused != 0 {
			t.Fatalf("headers=%v bodies=%v stats=%#v", headers, bodies, stats)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:2016
	t.Run("sends only response input deltas in websocket-cached mode", func(t *testing.T) {
		peer := newCodexWSPeer(t, func(index, _ int, _ map[string]any) []string {
			frames := []string{fmt.Sprintf(`{"type":"response.created","response":{"id":"resp_%d"}}`, index)}
			if index == 1 {
				frames = append(frames, `{"type":"response.output_item.added","item":{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"sample_tool","input":""}}`, `{"type":"response.custom_tool_call_input.delta","item_id":"ctc_1","delta":"abc"}`, `{"type":"response.custom_tool_call_input.done","item_id":"ctc_1","input":"abc"}`, `{"type":"response.output_item.done","item":{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"sample_tool","input":"abc"}}`)
			}
			return append(frames, codexWSTerminal(fmt.Sprintf("resp_%d", index)))
		})
		provider := peer.provider(t, "acc_test")
		provider.cfg.Compat.SupportsOpenAIGrammarTools = new(true)
		var tool ToolSchema
		if err := json.Unmarshal([]byte(`{"name":"sample_tool","description":"Sample tool","parameters":{"type":"object","properties":{"payload":{"type":"string"}},"required":["payload"]},"constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: /[a-z]+/"}}}`), &tool); err != nil {
			t.Fatal(err)
		}
		ctx := Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Use the tool"), Timestamp: 1}}, Tools: []ToolSchema{tool}}
		first, _ := codexWSResult(t, provider, NormalizeContext(ctx), StreamOptions{Transport: TransportWebSocketCached, SessionID: "session-1"})
		ctx.Messages = append(ctx.Messages, *first, ToolResultMessage{ToolCallID: "call_1|ctc_1", ToolName: "sample_tool", Content: []ToolResultMessageContent{TextContent{Text: "real result"}}, Timestamp: 2}, UserMessage{Content: UserText("Now finish"), Timestamp: 3})
		_, _ = codexWSResult(t, provider, NormalizeContext(ctx), StreamOptions{Transport: TransportWebSocketCached, SessionID: "session-1"})
		_, bodies, _ := peer.snapshot()
		if len(bodies) != 2 {
			t.Fatal(bodies)
		}
		for _, body := range bodies {
			if body.Body["store"] != false {
				t.Fatal(body)
			}
		}
		if bodies[0].Body["previous_response_id"] != nil || bodies[1].Body["previous_response_id"] != "resp_1" {
			t.Fatal(bodies)
		}
		assertCompletionsJSON(t, bodies[0].Body["input"], `[{"role":"user","content":[{"type":"input_text","text":"Use the tool"}]}]`)
		assertCompletionsJSON(t, bodies[1].Body["input"], `[{"type":"custom_tool_call_output","call_id":"call_1","output":"real result"},{"role":"user","content":[{"type":"input_text","text":"Now finish"}]}]`)
		stats := GetOpenAICodexWebSocketDebugStats("session-1")
		if stats == nil || stats.Requests != 2 || stats.ConnectionsCreated != 1 || stats.ConnectionsReused != 1 || stats.CachedContextRequests != 2 || stats.StoreTrueRequests != 0 || stats.FullContextRequests != 1 || stats.DeltaRequests != 1 || stats.LastDeltaInputItems == nil || *stats.LastDeltaInputItems != 2 || stats.LastPreviousResponseId != "resp_1" {
			t.Fatalf("stats=%#v", stats)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:2188
	for _, recovery := range []string{"websocket", "sse"} {
		t.Run("recovers a missing cached websocket continuation via "+recovery, func(t *testing.T) {
			session := "missing-continuation-" + recovery
			peer := newCodexWSPeer(t, func(index, _ int, _ map[string]any) []string {
				if index == 2 {
					return []string{`{"type":"codex.rate_limits","plan_type":"plus","rate_limits":{"allowed":true,"limit_reached":false,"primary":{"used_percent":7,"window_minutes":10080,"reset_after_seconds":556112,"reset_at":1785269351},"secondary":null},"code_review_rate_limits":null,"additional_rate_limits":null,"credits":{"has_credits":false,"unlimited":false,"balance":"0"},"promo":null}`, `{"type":"error","status":400,"error":{"code":"previous_response_not_found","message":"Previous response with id 'resp_1' not found.","param":"previous_response_id"}}`}
				}
				if index == 3 && recovery == "sse" {
					return []string{"close"}
				}
				if index == 1 {
					return codexWSHello("resp_1", "Hello")
				}
				return codexWSHello("resp_2", "Recovered")
			})
			provider := peer.provider(t, "acc_test")
			first, _ := codexWSResult(t, provider, codexUpstreamContext(), StreamOptions{Transport: TransportWebSocketCached, SessionID: session})
			messages := append(codexUpstreamContext().Messages(), *first, UserMessage{Content: UserText("Now finish"), Timestamp: 2})
			second, types := codexWSResult(t, provider, NormalizeContext(Context{Messages: messages}), StreamOptions{Transport: TransportWebSocketCached, SessionID: session})
			wantText := "Recovered"
			wantFetches := 0
			if recovery == "sse" {
				wantText = "Hello"
				wantFetches = 1
			}
			starts := 0
			for _, kind := range types {
				if kind == EventStart {
					starts++
				}
				if kind == EventError {
					t.Error("unexpected error event")
				}
			}
			headers, bodies, fetches := peer.snapshot()
			if second.StopReason != StopReasonStop || codexUpstreamText(second) != wantText || starts != 1 || len(headers) != 2 || len(bodies) != 3 || fetches != wantFetches {
				t.Fatalf("second=%#v events=%v headers=%v bodies=%v fetches=%d", second, types, headers, bodies, fetches)
			}
			if !reflect.DeepEqual([]int{bodies[0].Connection, bodies[1].Connection, bodies[2].Connection}, []int{1, 1, 2}) || bodies[1].Body["previous_response_id"] != "resp_1" || bodies[2].Body["previous_response_id"] != nil {
				t.Fatal(bodies)
			}
			assertCompletionsJSON(t, bodies[1].Body["input"], `[{"role":"user","content":[{"type":"input_text","text":"Now finish"}]}]`)
			full := bodies[2].Body["input"].([]any)
			if len(full) != 3 {
				t.Fatal(full)
			}
			assertCompletionsJSON(t, full[len(full)-1], `{"role":"user","content":[{"type":"input_text","text":"Now finish"}]}`)
			stats := GetOpenAICodexWebSocketDebugStats(session)
			if stats == nil || stats.Requests != 3 || stats.ConnectionsCreated != 2 || stats.ConnectionsReused != 1 || stats.FullContextRequests != 2 || stats.DeltaRequests != 1 || stats.WebSocketFailures != wantFetches || stats.SSEFallbacks != wantFetches {
				t.Fatalf("stats=%#v", stats)
			}
		})
	}
}

func TestCodexWebSocketTimeoutsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name             string
		connect, started bool
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1580
		{"falls back to SSE when websocket connect does not open before the connect timeout", true, false},
		// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1725
		{"falls back to SSE when a websocket is idle before the first event", false, false},
		// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1827
		{"errors when a websocket is idle after the stream started", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "ws-idle-before-start"
			if tc.connect {
				session = "ws-connect-timeout"
			}
			if tc.started {
				session = ""
			}
			var mu sync.Mutex
			fetches, sends := 0, 0
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !websocket.IsWebSocketUpgrade(r) {
					mu.Lock()
					fetches++
					mu.Unlock()
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, codexUpstreamSSE("completed", nil))
					return
				}
				if tc.connect {
					<-release
					return
				}
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				mu.Lock()
				sends++
				mu.Unlock()
				if tc.started {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`))
				}
				<-release
			}))
			defer server.Close()
			defer close(release)
			defer CloseOpenAICodexWebSocketSessions(session)
			defer ResetOpenAICodexWebSocketDebugStats(session)
			provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acc_test"), Model: "gpt-5.1-codex", ProviderID: "openai-codex", BaseURL: server.URL})
			options := StreamOptions{Transport: TransportAuto, SessionID: session, TimeoutMs: new(50)}
			if tc.connect {
				options.TimeoutMs = new(300000)
				options.WebSocketConnectTimeoutMs = new(50)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			stream, err := provider.Stream(ctx, codexUpstreamContext(), options)
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			mu.Lock()
			gotFetches, gotSends := fetches, sends
			mu.Unlock()
			stats := GetOpenAICodexWebSocketDebugStats(session)
			if tc.started {
				if result.StopReason != StopReasonError || result.ErrorMessage != "WebSocket idle timeout after 50ms" || gotFetches != 0 {
					t.Fatalf("result=%#v fetches=%d", result, gotFetches)
				}
			} else {
				if codexUpstreamText(result) != "Hello" || gotFetches != 1 || stats == nil || stats.WebSocketFailures != 1 || stats.SSEFallbacks != 1 || !stats.WebSocketFallbackActive {
					t.Fatalf("result=%#v fetches=%d stats=%#v", result, gotFetches, stats)
				}
				if tc.connect {
					if stats.LastWebSocketError != "WebSocket connect timeout after 50ms" {
						t.Fatal(stats.LastWebSocketError)
					}
				} else if gotSends != 1 {
					t.Fatalf("sent=%d", gotSends)
				}
			}
		})
	}
}
