package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type piMessagesRecordedRequest struct {
	url     string
	headers http.Header
	body    map[string]any
}

type piMessagesResponder struct {
	status  int
	headers map[string]string
	events  []any
	rawBody string
}

// startPiMessagesServer mirrors the upstream pi-messages.test.ts startServer
// helper: it records each request and answers with SSE events or an error body.
func startPiMessagesServer(t *testing.T, responder piMessagesResponder) (string, func() []piMessagesRecordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []piMessagesRecordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		var body map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		mu.Lock()
		requests = append(requests, piMessagesRecordedRequest{url: request.URL.RequestURI(), headers: request.Header.Clone(), body: body})
		mu.Unlock()
		if responder.status != 0 && responder.status != http.StatusOK {
			writer.Header().Set("content-type", "application/json")
			writer.WriteHeader(responder.status)
			_, _ = io.WriteString(writer, responder.rawBody)
			return
		}
		writer.Header().Set("content-type", "text/event-stream")
		for name, value := range responder.headers {
			writer.Header().Set(name, value)
		}
		for _, event := range responder.events {
			data, _ := json.Marshal(event)
			_, _ = io.WriteString(writer, "data: "+string(data)+"\n\n")
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/v1", func() []piMessagesRecordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]piMessagesRecordedRequest(nil), requests...)
	}
}

var piMessagesTestUsage = map[string]any{
	"input": 10, "output": 5, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 15,
	"cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0, "cacheWrite": 0, "total": 0.3},
}

var piMessagesWantUsage = Usage{Input: 10, Output: 5, TotalTokens: 15, Cost: UsageCost{Input: 0.1, Output: 0.2, Total: 0.3}}

func piMessagesTestContext() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}})
}

func newTestPiMessagesProvider(baseURL string, configure func(*PiMessagesConfig)) Provider {
	cfg := PiMessagesConfig{BaseURL: baseURL, APIKey: "test-key", Model: "auto", ProviderID: "radius"}
	if configure != nil {
		configure(&cfg)
	}
	return NewPiMessagesProvider(cfg)
}

func collectPiMessages(t *testing.T, provider Provider, opts StreamOptions) ([]AssistantMessageEvent, *AssistantMessage) {
	t.Helper()
	stream, err := provider.Stream(context.Background(), piMessagesTestContext(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(context.Background()) {
		events = append(events, event)
	}
	return events, stream.Result()
}

// Ported from pi-messages.test.ts "streams text and tool calls and resolves the terminal message".
func TestPiMessagesStreamsTextAndToolCalls(t *testing.T) {
	baseURL, requests := startPiMessagesServer(t, piMessagesResponder{events: []any{
		map[string]any{"type": "start"},
		map[string]any{"type": "text_start", "contentIndex": 0},
		map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "Hel"},
		map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "lo"},
		map[string]any{"type": "text_end", "contentIndex": 0, "content": "Hello"},
		map[string]any{"type": "toolcall_start", "contentIndex": 1, "id": "call_1", "toolName": "read"},
		map[string]any{"type": "toolcall_delta", "contentIndex": 1, "delta": `{"path":`},
		map[string]any{"type": "toolcall_delta", "contentIndex": 1, "delta": `"a.txt"}`},
		map[string]any{"type": "toolcall_end", "contentIndex": 1, "toolCall": map[string]any{"type": "toolCall", "id": "call_1", "name": "read", "arguments": map[string]any{"path": "a.txt"}}},
		map[string]any{"type": "done", "reason": "toolUse", "usage": piMessagesTestUsage, "responseId": "resp_1", "providerThinkingLevel": "high"},
	}})
	provider := newTestPiMessagesProvider(baseURL, nil)

	events, message := collectPiMessages(t, provider, StreamOptions{SessionID: "session-1", MaxTokens: 100, Headers: ProviderHeaders{"x-custom": new("1")}})

	if start, ok := events[0].(StartEvent); !ok || start.Partial.StopReason != StopReasonPending {
		t.Fatalf("first event = %#v", events[0])
	}
	if message.StopReason != StopReasonToolUse || message.Usage != piMessagesWantUsage || message.ResponseID != "resp_1" || message.ProviderThinkingLevel != "high" || message.Model != "auto" || message.Provider != "radius" {
		t.Fatalf("message = %+v", message)
	}
	wantContent := []AssistantContentBlock{TextContent{Text: "Hello"}, ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{"path": "a.txt"}}}
	if !reflect.DeepEqual(message.Content, wantContent) {
		t.Fatalf("content = %#v", message.Content)
	}
	deltas, toolEnds := 0, 0
	for _, event := range events {
		switch event.(type) {
		case TextDeltaEvent:
			deltas++
		case ToolCallEndEvent:
			toolEnds++
		}
	}
	if deltas == 0 || toolEnds != 1 {
		t.Fatalf("text deltas = %d, toolcall_end = %d", deltas, toolEnds)
	}

	recorded := requests()
	if len(recorded) != 1 {
		t.Fatalf("requests = %d", len(recorded))
	}
	request := recorded[0]
	if request.url != "/v1/messages" || request.headers.Get("authorization") != "Bearer test-key" || request.headers.Get("x-custom") != "1" || request.headers.Get("accept") != "text/event-stream" {
		t.Fatalf("request url=%q headers=%v", request.url, request.headers)
	}
	wantBody := map[string]any{
		"model":   "auto",
		"context": map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Hello", "timestamp": float64(1)}}},
		"options": map[string]any{"maxTokens": float64(100), "sessionId": "session-1"},
	}
	if !reflect.DeepEqual(request.body, wantBody) {
		t.Fatalf("body = %#v", request.body)
	}
}

// Ported from pi-messages.test.ts "appends debug=1 and reports response headers via onResponse".
func TestPiMessagesDebugAndOnResponse(t *testing.T) {
	baseURL, requests := startPiMessagesServer(t, piMessagesResponder{
		headers: map[string]string{"x-pi-gateway-upstream-provider": "anthropic"},
		events:  []any{map[string]any{"type": "done", "reason": "stop", "usage": piMessagesTestUsage}},
	})
	var observed map[string]string
	provider := newTestPiMessagesProvider(baseURL, func(cfg *PiMessagesConfig) {
		cfg.Debug = true
		cfg.ToolChoice = "auto"
		cfg.OnResponse = func(response PiMessagesResponse) error {
			observed = response.Headers
			return nil
		}
	})

	_, message := collectPiMessages(t, provider, StreamOptions{})

	if message.StopReason != StopReasonStop {
		t.Fatalf("message = %+v", message)
	}
	recorded := requests()
	if recorded[0].url != "/v1/messages?debug=1" || observed["x-pi-gateway-upstream-provider"] != "anthropic" {
		t.Fatalf("url = %q, headers = %v", recorded[0].url, observed)
	}
	if options := recorded[0].body["options"].(map[string]any); options["toolChoice"] != "auto" {
		t.Fatalf("options = %v", options)
	}
}

// Ported from pi-messages.test.ts "surfaces backend error responses with diagnostics".
func TestPiMessagesSurfacesBackendErrorResponses(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{status: 401, rawBody: `{"error":{"message":"Token expired","code":"unauthorized"}}`})

	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, func(cfg *PiMessagesConfig) { cfg.APIKey = "stale" }), StreamOptions{})

	if message.StopReason != StopReasonError || message.ErrorMessage != "401 Unauthorized: Token expired (unauthorized)" {
		t.Fatalf("message = %+v", message)
	}
	if len(message.Diagnostics) != 1 || message.Diagnostics[0].Type != "pi_messages_response_failure" || message.Diagnostics[0].Details["status"] != 401 {
		t.Fatalf("diagnostics = %+v", message.Diagnostics)
	}
	if message.Diagnostics[0].Error == nil || message.Diagnostics[0].Error.Code != "unauthorized" {
		t.Fatalf("diagnostic error = %+v", message.Diagnostics[0].Error)
	}
}

func TestPiMessagesUnstructuredErrorBodyIsTruncatedInDiagnostics(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{status: 502, rawBody: strings.Repeat("x", 9000)})

	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})

	body, _ := message.Diagnostics[0].Details["body"].(string)
	if !strings.HasPrefix(message.ErrorMessage, "502 Bad Gateway: xxx") || len(body) != 8192+len("…") {
		t.Fatalf("error = %.40q, diagnostic body length = %d", message.ErrorMessage, len(body))
	}
}

// Ported from pi-messages.test.ts "propagates server-sent error events".
func TestPiMessagesPropagatesServerErrorEvents(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{
		map[string]any{"type": "start"},
		map[string]any{"type": "error", "reason": "error", "usage": piMessagesTestUsage, "errorMessage": "Upstream failed"},
	}})

	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})

	if message.StopReason != StopReasonError || message.ErrorMessage != "Upstream failed" || message.Usage != piMessagesWantUsage {
		t.Fatalf("message = %+v", message)
	}
}

// Ported from pi-messages.test.ts "errors when no API key is provided".
func TestPiMessagesErrorsWithoutAPIKey(t *testing.T) {
	provider := newTestPiMessagesProvider("http://127.0.0.1:1/v1", func(cfg *PiMessagesConfig) { cfg.APIKey = "" })

	_, message := collectPiMessages(t, provider, StreamOptions{})

	if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "No API key provided") {
		t.Fatalf("message = %+v", message)
	}
}

// Ported from pi-messages.test.ts "errors when the stream ends without a terminal event".
func TestPiMessagesErrorsWhenStreamEndsWithoutTerminal(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{
		map[string]any{"type": "start"},
		map[string]any{"type": "text_start", "contentIndex": 0},
		map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "partial"},
	}})

	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})

	if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "stream ended without a terminal event") {
		t.Fatalf("message = %+v", message)
	}
}

func TestPiMessagesCacheRetentionPrecedence(t *testing.T) {
	environments := []struct {
		name  string
		value string
	}{
		{name: "absent"},
		{name: "long", value: "long"},
	}
	options := []struct {
		name  string
		value CacheRetention
	}{
		{name: "unset"},
		{name: "none", value: CacheRetentionNone},
		{name: "short", value: CacheRetentionShort},
		{name: "long", value: CacheRetentionLong},
	}

	for _, environment := range environments {
		for _, option := range options {
			t.Run("env="+environment.name+"/option="+option.name, func(t *testing.T) {
				t.Setenv("PI_CACHE_RETENTION", environment.value)
				provider := &piMessagesProvider{cfg: PiMessagesConfig{Model: "auto", ProviderID: "radius"}}
				payload, err := provider.payload(piMessagesTestContext(), StreamOptions{CacheRetention: option.value})
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				if err := json.Unmarshal(payload, &body); err != nil {
					t.Fatal(err)
				}
				serialized, present := body["options"].(map[string]any)["cacheRetention"]
				want := option.value
				if want == "" && environment.value == "long" {
					want = CacheRetentionLong
				}
				if want == "" {
					if present {
						t.Fatalf("cacheRetention = %v, want omitted; payload = %s", serialized, payload)
					}
					return
				}
				if !present || serialized != string(want) {
					t.Fatalf("cacheRetention = %v (present %v), want %q; payload = %s", serialized, present, want, payload)
				}
			})
		}
	}
}

func TestPiMessagesThinkingRewriteAndRequestOptions(t *testing.T) {
	baseURL, requests := startPiMessagesServer(t, piMessagesResponder{events: []any{
		map[string]any{"type": "thinking_start", "contentIndex": 0},
		map[string]any{"type": "thinking_delta", "contentIndex": 0, "delta": "hm"},
		map[string]any{"type": "thinking_end", "contentIndex": 0, "content": "hmm", "contentSignature": "sig", "redacted": true},
		map[string]any{"type": "done", "reason": "stop", "usage": piMessagesTestUsage, "rewrite": map[string]any{"policyId": "p", "policyVersion": 2, "changed": true, "tokenCountChange": -5, "messageCountChange": 0, "systemPromptChanged": false}},
	}})
	t.Setenv("PI_CACHE_RETENTION", "long")

	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{Thinking: ThinkingHigh, Temperature: 0.5})

	if !reflect.DeepEqual(message.Content, []AssistantContentBlock{ThinkingContent{Thinking: "hmm", ThinkingSignature: "sig", Redacted: true}}) {
		t.Fatalf("content = %#v", message.Content)
	}
	if len(message.Diagnostics) != 1 || message.Diagnostics[0].Type != "pi_messages_rewrite" || message.Diagnostics[0].Details["policyId"] != "p" {
		t.Fatalf("diagnostics = %+v", message.Diagnostics)
	}
	options := requests()[0].body["options"].(map[string]any)
	if options["reasoning"] != "high" || options["temperature"] != 0.5 || options["cacheRetention"] != "long" {
		t.Fatalf("options = %v", options)
	}
}

func TestPiMessagesAbortReportsAborted(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"type\":\"start\"}\n\n")
		writer.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := newTestPiMessagesProvider(server.URL, nil).Stream(ctx, piMessagesTestContext(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for event := range stream.Events(context.Background()) {
		if _, ok := event.(StartEvent); ok {
			cancel()
		}
	}
	if message := stream.Result(); message.StopReason != StopReasonAborted || len(message.Diagnostics) != 0 {
		t.Fatalf("message = %+v", message)
	}
}

// Ported from pi-messages.test.ts "pi-messages api registration".
func TestPiMessagesIsRegisteredBuiltinAPI(t *testing.T) {
	factory, ok := LookupBuiltInProvider(APIPiMessages)
	if !ok {
		t.Fatal("pi-messages is not a registered built-in API")
	}
	if provider := factory("key", "auto", "http://127.0.0.1:1/v1"); provider == nil || provider.ID() != string(APIPiMessages) {
		t.Fatalf("factory provider = %#v", provider)
	}
}
