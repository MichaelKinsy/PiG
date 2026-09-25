package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func proxyTestModel() *ai.Model {
	strict := true
	return &ai.Model{
		ID:          "gpt-5.4",
		DisplayName: "GPT-5.4",
		Capabilities: ai.ModelCapabilities{
			MaxThinking:         ai.ThinkingHigh,
			SupportsImages:      true,
			ContextWindow:       400_000,
			MaxOutputTokens:     128_000,
			InputCostPer1M:      1.25,
			OutputCostPer1M:     10,
			CacheReadCostPer1M:  0.125,
			CacheWriteCostPer1M: 1.5,
		},
		ProviderMeta: ai.ProviderMetadata{
			ProviderID: "openai",
			API:        ai.APIOpenAIResponses,
			BaseURL:    "https://api.openai.com/v1",
			Reasoning:  true,
			Headers:    map[string]string{"X-Model": "header"},
			Compat:     &ai.OpenAICompat{SupportsStrictMode: &strict},
		},
		Input:          []string{"text", "image"},
		SamplingParams: map[string]any{"top_p": 0.9},
		PromptCache:    ai.ModelPromptCache{"short": 300},
	}
}

func proxyTestUsage() ai.Usage {
	oneHour := 2
	reasoning := 7
	return ai.Usage{
		Input: 11, Output: 13, CacheRead: 3, CacheWrite: 5,
		CacheWrite1h: &oneHour, Reasoning: &reasoning, TotalTokens: 32,
		Cost: ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
}

func writeProxyEvents(t *testing.T, writer http.ResponseWriter, events []ProxyAssistantMessageEvent, terminateLastLine bool) {
	t.Helper()
	writer.Header().Set("Content-Type", "text/event-stream")
	for index, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write([]byte("data: "))
		_, _ = writer.Write(encoded)
		if terminateLastLine || index < len(events)-1 {
			_, _ = writer.Write([]byte("\n\n"))
		}
	}
}

func collectProxyEvents(t *testing.T, stream *ai.AssistantMessageEventStream) ([]ai.AssistantMessageEvent, *ai.AssistantMessage) {
	t.Helper()
	var events []ai.AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	return events, stream.Result()
}

func proxyEventTypes(events []ai.AssistantMessageEvent) []ai.AssistantEventType {
	types := make([]ai.AssistantEventType, len(events))
	for index, event := range events {
		types[index] = event.EventType()
	}
	return types
}

func TestStreamProxyReconstructsPartialsAndPreservesRequestAndTerminalMetadata(t *testing.T) {
	usage := proxyTestUsage()
	signature := "sig-text"
	thinkingSignature := "sig-thinking"
	providerThinkingLevel := "high"
	requestBody := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/stream" {
			t.Errorf("request = %s %s, want POST /api/stream", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requestBody <- payload
		writeProxyEvents(t, writer, []ProxyAssistantMessageEvent{
			{Type: "start"},
			{Type: "text_start", ContentIndex: 0},
			{Type: "text_delta", ContentIndex: 0, Delta: "hello "},
			{Type: "text_delta", ContentIndex: 0, Delta: "世界"},
			{Type: "text_end", ContentIndex: 0, ContentSignature: &signature},
			{Type: "thinking_start", ContentIndex: 1},
			{Type: "thinking_delta", ContentIndex: 1, Delta: "reason"},
			{Type: "thinking_end", ContentIndex: 1, ContentSignature: &thinkingSignature},
			{Type: "toolcall_start", ContentIndex: 2, ID: "call_test|fc_test", ToolName: "lookup"},
			{Type: "toolcall_delta", ContentIndex: 2, Delta: `{"value":"hel`},
			{Type: "toolcall_delta", ContentIndex: 2, Delta: `lo"}`},
			{Type: "toolcall_end", ContentIndex: 2, ToolCall: &ai.ToolCall{
				ID: "call_test|fc_test", Name: "lookup", Arguments: ai.JsonObject{"value": "hello"},
				ThoughtSignature: "thought", Namespace: "dynamic_tools",
			}},
			{Type: "done", Reason: ai.StopReasonToolUse, Usage: usage, ProviderThinkingLevel: &providerThinkingLevel},
		}, false)
	}))
	defer server.Close()

	temperature := 0.0
	maxTokens := 123
	maxRetryDelayMs := 0
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{
		ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 10},
	}})
	stream := StreamProxy(t.Context(), proxyTestModel(), transcript, ProxyStreamOptions{
		AuthToken: "test-token", ProxyURL: server.URL,
		Temperature: &temperature, SamplingParams: map[string]any{"top_k": 4}, MaxTokens: &maxTokens,
		Reasoning: ai.ThinkingHigh, CacheRetention: ai.CacheRetentionLong, SessionID: "session-1",
		Headers:  ai.ProviderHeaders{"X-Request": new("yes"), "X-Remove": nil},
		Metadata: map[string]any{"user_id": "user-1"}, Transport: ai.TransportSSE,
		ThinkingBudgets: &ProxyThinkingBudgets{Minimal: new(1), Low: new(2), Medium: new(3), High: new(4)},
		MaxRetryDelayMs: &maxRetryDelayMs,
	})
	events, result := collectProxyEvents(t, stream)

	wantTypes := []ai.AssistantEventType{
		ai.EventStart, ai.EventTextStart, ai.EventTextDelta, ai.EventTextDelta, ai.EventTextEnd,
		ai.EventThinkingStart, ai.EventThinkingDelta, ai.EventThinkingEnd,
		ai.EventToolCallStart, ai.EventToolCallDelta, ai.EventToolCallDelta, ai.EventToolCallEnd, ai.EventDone,
	}
	if got := proxyEventTypes(events); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	if result == nil {
		t.Fatal("stream result is nil")
	}
	if result.API != ai.APIOpenAIResponses || result.Provider != "openai" || result.Model != "gpt-5.4" {
		t.Errorf("result identity = api %q provider %q model %q", result.API, result.Provider, result.Model)
	}
	if result.StopReason != ai.StopReasonToolUse || !reflect.DeepEqual(result.Usage, usage) || result.ProviderThinkingLevel != "high" {
		t.Errorf("terminal metadata = reason %q usage %+v thinking %q", result.StopReason, result.Usage, result.ProviderThinkingLevel)
	}
	if got, ok := result.Content[0].(ai.TextContent); !ok || got.Text != "hello 世界" || got.TextSignature != signature {
		t.Errorf("text block = %#v", result.Content[0])
	}
	if got, ok := result.Content[1].(ai.ThinkingContent); !ok || got.Thinking != "reason" || got.ThinkingSignature != thinkingSignature {
		t.Errorf("thinking block = %#v", result.Content[1])
	}
	toolCall, ok := result.Content[2].(ai.ToolCall)
	if !ok || toolCall.Namespace != "dynamic_tools" || toolCall.ThoughtSignature != "thought" || toolCall.Arguments["value"] != "hello" {
		t.Errorf("tool call = %#v", result.Content[2])
	}
	firstToolDelta := events[9].(ai.ToolCallDeltaEvent)
	if got := firstToolDelta.Partial.Content[2].(ai.ToolCall).Arguments["value"]; got != "hel" {
		t.Errorf("first partial tool argument = %#v, want hel", got)
	}

	payload := <-requestBody
	model := payload["model"].(map[string]any)
	if model["id"] != "gpt-5.4" || model["name"] != "GPT-5.4" || model["api"] != "openai-responses" || model["provider"] != "openai" || model["baseUrl"] != "https://api.openai.com/v1" {
		t.Errorf("proxy model identity = %#v", model)
	}
	if model["reasoning"] != true || model["contextWindow"] != float64(400_000) || model["maxTokens"] != float64(128_000) {
		t.Errorf("proxy model capabilities = %#v", model)
	}
	contextPayload := payload["context"].(map[string]any)
	messages := contextPayload["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["role"] != "user" {
		t.Errorf("proxy context = %#v", contextPayload)
	}
	options := payload["options"].(map[string]any)
	for key, want := range map[string]any{
		"temperature": float64(0), "maxTokens": float64(123), "reasoning": "high", "cacheRetention": "long",
		"sessionId": "session-1", "transport": "sse", "maxRetryDelayMs": float64(0),
	} {
		if got := options[key]; got != want {
			t.Errorf("options[%q] = %#v, want %#v", key, got, want)
		}
	}
	if got := options["samplingParams"].(map[string]any)["top_k"]; got != float64(4) {
		t.Errorf("samplingParams.top_k = %#v", got)
	}
	if got := options["metadata"].(map[string]any)["user_id"]; got != "user-1" {
		t.Errorf("metadata.user_id = %#v", got)
	}
	headers := options["headers"].(map[string]any)
	if headers["X-Request"] != "yes" || headers["X-Remove"] != nil {
		t.Errorf("headers = %#v", headers)
	}
	if got := options["thinkingBudgets"].(map[string]any); !reflect.DeepEqual(got, map[string]any{
		"minimal": float64(1), "low": float64(2), "medium": float64(3), "high": float64(4),
	}) {
		t.Errorf("thinkingBudgets = %#v", got)
	}
	if _, leaked := options["authToken"]; leaked {
		t.Error("proxy request options leaked authToken")
	}
	if _, leaked := options["proxyUrl"]; leaked {
		t.Error("proxy request options leaked proxyUrl")
	}
}

func TestStreamProxyOmitsUnsetOptionsAndSendsEmptyContextArray(t *testing.T) {
	requestBody := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requestBody <- payload
		writeProxyEvents(t, writer, []ProxyAssistantMessageEvent{
			{Type: "start"},
			{Type: "done", Reason: ai.StopReasonStop, Usage: ai.Usage{}},
		}, true)
	}))
	defer server.Close()

	_, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	}))
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("result = %+v", result)
	}
	payload := <-requestBody
	contextPayload := payload["context"].(map[string]any)
	if messages, ok := contextPayload["messages"].([]any); !ok || len(messages) != 0 {
		t.Errorf("empty context messages = %#v, want []", contextPayload["messages"])
	}
	if options, ok := payload["options"].(map[string]any); !ok || len(options) != 0 {
		t.Errorf("unset options = %#v, want {}", payload["options"])
	}
}

// Upstream buildProxyRequestOptions copies present objects and zero budgets unchanged; JSON.stringify omits only undefined options.
func TestProxyRequestPreservesOptionPresence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options ProxyStreamOptions
		want    string
	}{
		{name: "unset", want: `{}`},
		{
			name: "empty objects and zero minimal budget",
			options: ProxyStreamOptions{
				SamplingParams: map[string]any{}, Headers: ai.ProviderHeaders{}, Metadata: map[string]any{},
				ThinkingBudgets: &ProxyThinkingBudgets{Minimal: new(0), Low: new(2), Medium: new(3), High: new(4)},
			},
			want: `{"samplingParams":{},"headers":{},"metadata":{},"thinkingBudgets":{"minimal":0,"low":2,"medium":3,"high":4}}`,
		},
		{
			name:    "all zero budgets",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{Minimal: new(0), Low: new(0), Medium: new(0), High: new(0)}},
			want:    `{"thinkingBudgets":{"minimal":0,"low":0,"medium":0,"high":0}}`,
		},
		{
			name:    "high budget with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{High: new(4096)}},
			want:    `{"thinkingBudgets":{"high":4096}}`,
		},
		{
			name:    "empty budgets",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{}},
			want:    `{"thinkingBudgets":{}}`,
		},
		{
			name:    "minimal budget with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{Minimal: new(4096)}},
			want:    `{"thinkingBudgets":{"minimal":4096}}`,
		},
		{
			name:    "low budget with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{Low: new(4096)}},
			want:    `{"thinkingBudgets":{"low":4096}}`,
		},
		{
			name:    "medium budget with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{Medium: new(4096)}},
			want:    `{"thinkingBudgets":{"medium":4096}}`,
		},
		{
			name:    "zero minimal with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{Minimal: new(0)}},
			want:    `{"thinkingBudgets":{"minimal":0}}`,
		},
		{
			name:    "zero low with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{Low: new(0)}},
			want:    `{"thinkingBudgets":{"low":0}}`,
		},
		{
			name:    "zero medium with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{Medium: new(0)}},
			want:    `{"thinkingBudgets":{"medium":0}}`,
		},
		{
			name:    "zero high with omitted siblings",
			options: ProxyStreamOptions{ThinkingBudgets: &ProxyThinkingBudgets{High: new(0)}},
			want:    `{"thinkingBudgets":{"high":0}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transcript := ai.NormalizeContext(ai.Context{})
			t.Run("build", func(t *testing.T) {
				body, err := buildProxyRequest(proxyTestModel(), transcript, tc.options)
				if err != nil {
					t.Fatal(err)
				}
				var payload struct {
					Options json.RawMessage `json:"options"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if got := string(payload.Options); got != tc.want {
					t.Errorf("options = %s, want %s", got, tc.want)
				}
			})
			t.Run("stream", func(t *testing.T) {
				requestOptions := make(chan json.RawMessage, 1)
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					var payload struct {
						Options json.RawMessage `json:"options"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Errorf("decode request: %v", err)
					}
					requestOptions <- payload.Options
					writeProxyEvents(t, writer, []ProxyAssistantMessageEvent{
						{Type: "start"},
						{Type: "done", Reason: ai.StopReasonStop},
					}, false)
				}))
				defer server.Close()

				options := tc.options
				options.AuthToken = "token"
				options.ProxyURL = server.URL
				_, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), transcript, options))
				if result.StopReason != ai.StopReasonStop {
					t.Fatalf("result = %+v", result)
				}
				if got := string(<-requestOptions); got != tc.want {
					t.Errorf("options = %s, want %s", got, tc.want)
				}
			})
		})
	}
}

// Upstream processProxyEvent gives each toolcall_start a new block with partialJson: "", even when its index replaces an unfinished tool.
func TestProxyEventConverterRestartsToolIndex(t *testing.T) {
	for _, staleJSON := range []string{`{"stale":1}`, `{"stale":"unfinished`} {
		t.Run(staleJSON, func(t *testing.T) {
			converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolJSON: map[int]string{}}
			for _, event := range []ProxyAssistantMessageEvent{
				{Type: "toolcall_start", ContentIndex: 0, ID: "old", ToolName: "lookup"},
				{Type: "toolcall_delta", ContentIndex: 0, Delta: staleJSON},
				{Type: "toolcall_start", ContentIndex: 0, ID: "new", ToolName: "lookup"},
			} {
				if _, err := converter.process(event); err != nil {
					t.Fatal(err)
				}
			}
			if got := converter.toolJSON[0]; got != "" {
				t.Errorf("restarted tool JSON = %q, want empty", got)
			}
			if got, want := converter.partial.Content[0], (ai.ToolCall{ID: "new", Name: "lookup", Arguments: ai.JsonObject{}}); !reflect.DeepEqual(got, want) {
				t.Errorf("restarted tool = %#v, want %#v", got, want)
			}
			for _, delta := range []string{`{"fresh":2`, `}`} {
				event, err := converter.process(ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: delta})
				if err != nil {
					t.Fatal(err)
				}
				got := event.(ai.ToolCallDeltaEvent).Partial.Content[0]
				want := ai.ToolCall{ID: "new", Name: "lookup", Arguments: ai.JsonObject{"fresh": float64(2)}}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("tool after delta %q = %#v, want %#v", delta, got, want)
				}
			}
		})
	}
}

func TestStreamProxyRestartsToolIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeProxyEvents(t, writer, []ProxyAssistantMessageEvent{
			{Type: "start"},
			{Type: "toolcall_start", ContentIndex: 0, ID: "old", ToolName: "lookup"},
			{Type: "toolcall_delta", ContentIndex: 0, Delta: `{"stale":1}`},
			{Type: "toolcall_start", ContentIndex: 0, ID: "new", ToolName: "lookup"},
			{Type: "toolcall_delta", ContentIndex: 0, Delta: `{"fresh":2}`},
			{Type: "done", Reason: ai.StopReasonToolUse},
		}, false)
	}))
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	}))
	wantTypes := []ai.AssistantEventType{
		ai.EventStart, ai.EventToolCallStart, ai.EventToolCallDelta, ai.EventToolCallStart, ai.EventToolCallDelta, ai.EventDone,
	}
	if got := proxyEventTypes(events); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	if result.StopReason != ai.StopReasonToolUse {
		t.Fatalf("result = %+v", result)
	}
	for _, check := range []struct {
		name    string
		partial *ai.AssistantMessage
		want    ai.ToolCall
	}{
		{"old delta", events[2].(ai.ToolCallDeltaEvent).Partial, ai.ToolCall{ID: "old", Name: "lookup", Arguments: ai.JsonObject{"stale": json.Number("1")}}},
		{"new start", events[3].(ai.ToolCallStartEvent).Partial, ai.ToolCall{ID: "new", Name: "lookup", Arguments: ai.JsonObject{}}},
		{"new delta", events[4].(ai.ToolCallDeltaEvent).Partial, ai.ToolCall{ID: "new", Name: "lookup", Arguments: ai.JsonObject{"fresh": json.Number("2")}}},
		{"result", result, ai.ToolCall{ID: "new", Name: "lookup", Arguments: ai.JsonObject{"fresh": float64(2)}}},
	} {
		if got, want := check.partial.Content, []ai.AssistantContentBlock{check.want}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s content = %#v, want %#v", check.name, got, want)
		}
	}
}

func TestStreamProxyPreservesProxyErrorUsage(t *testing.T) {
	usage := proxyTestUsage()
	providerThinkingLevel := "medium"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		message := "provider rejected request"
		writeProxyEvents(t, writer, []ProxyAssistantMessageEvent{
			{Type: "start"},
			{Type: "error", Reason: ai.StopReasonError, ErrorMessage: &message, Usage: usage, ProviderThinkingLevel: &providerThinkingLevel},
		}, true)
	}))
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	}))
	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventError}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "provider rejected request" || !reflect.DeepEqual(result.Usage, usage) || result.ProviderThinkingLevel != "medium" {
		t.Fatalf("error result = %+v", result)
	}
}

func TestStreamProxyErrorsOnCleanEOFWithoutTerminalEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeProxyEvents(t, writer, []ProxyAssistantMessageEvent{{Type: "start"}}, true)
	}))
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	}))
	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventError}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Connection closed by proxy server before the response completed" {
		t.Fatalf("EOF result = %+v", result)
	}
}

func TestStreamProxyUsesStructuredHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"expired token"}`))
	}))
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	}))
	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventError}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Proxy error: expired token" {
		t.Fatalf("HTTP error result = %+v", result)
	}
}

func TestStreamProxyMalformedDeltaTerminatesWithError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeProxyEvents(t, writer, []ProxyAssistantMessageEvent{
			{Type: "start"},
			{Type: "text_delta", ContentIndex: 0, Delta: "orphan"},
		}, true)
	}))
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	}))
	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventError}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Received text_delta for non-text content" {
		t.Fatalf("malformed delta result = %+v", result)
	}
}

func TestStreamProxyCancellationTerminatesAsAborted(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"start\"}\n\n"))
		writer.(http.Flusher).Flush()
		close(requestStarted)
		<-request.Context().Done()
		close(requestCancelled)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	stream := StreamProxy(ctx, proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	})
	resultReady := make(chan *ai.AssistantMessage, 1)
	go func() {
		for range stream.Events(context.Background()) {
		}
		resultReady <- stream.Result()
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy request did not start")
	}
	cancel()
	select {
	case result := <-resultReady:
		if result.StopReason != ai.StopReasonAborted || result.ErrorMessage != "Request aborted by user" {
			t.Fatalf("cancelled result = %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled proxy stream did not terminate")
	}
	select {
	case <-requestCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reach proxy request")
	}
}
