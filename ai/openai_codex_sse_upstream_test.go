package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

func codexUpstreamSSE(status string, endTurn *bool) string {
	terminal := "response.completed"
	details := any(nil)
	if status == "incomplete" {
		terminal = "response.incomplete"
		details = map[string]any{"reason": "max_output_tokens"}
	}
	response := map[string]any{"status": status, "incomplete_details": details, "usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8, "input_tokens_details": map[string]any{"cached_tokens": 0}}}
	if endTurn != nil {
		response["end_turn"] = *endTurn
	}
	events := []any{
		map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}}},
		map[string]any{"type": "response.content_part.added", "part": map[string]any{"type": "output_text", "text": ""}},
		map[string]any{"type": "response.output_text.delta", "delta": "Hello"},
		map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Hello"}}}},
		map[string]any{"type": terminal, "response": response},
	}
	var out strings.Builder
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		fmt.Fprintf(&out, "data: %s\n\n", encoded)
	}
	return out.String()
}
func codexUpstreamContext() TranscriptContext {
	return NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Say hello"), Timestamp: 1}}})
}
func codexUpstreamProvider(t *testing.T, id string, transport http.RoundTripper) *openAIResponsesProvider {
	t.Helper()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acc_test"), Model: id, ModelMetadata: &Model{ID: id, DisplayName: id, ProviderMeta: ProviderMetadata{API: APIOpenAICodexResponses, ProviderID: "openai-codex", Reasoning: true}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 400000, MaxOutputTokens: 128000, MaxThinking: ThinkingLevelHigh}}, ProviderID: "openai-codex"}).(*openAIResponsesProvider)
	if transport != nil {
		provider.client = &http.Client{Transport: transport}
	}
	t.Cleanup(func() { _ = provider.Close() })
	return provider
}
func codexUpstreamHTTP(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func codexUpstreamBody(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("Content-Encoding") == "zstd" {
		body, err = decodeZstdRawFrameForTest(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
func codexUpstreamText(result *AssistantMessage) string {
	for _, block := range result.Content {
		if text, ok := block.(TextContent); ok {
			return text.Text
		}
	}
	return ""
}
func TestCodexSSEAndPayloadUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/openai-codex-stream.test.ts:101
	t.Run("streams SSE responses and forwards raw provider events", func(t *testing.T) {
		provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://chatgpt.com/backend-api/codex/responses" {
				t.Errorf("URL=%s", r.URL)
			}
			// D65 changes only product identity. The protocol and platform fields remain observable.
			for key, want := range map[string]string{"Authorization": "Bearer " + codexTestToken(t, "acc_test"), "chatgpt-account-id": "acc_test", "OpenAI-Beta": "responses=experimental", "originator": pigidentity.CodexOriginator, "User-Agent": PiUserAgent(), "accept": "text/event-stream"} {
				if r.Header.Get(key) != want {
					t.Errorf("%s=%q want %q", key, r.Header.Get(key), want)
				}
			}
			if _, ok := r.Header["X-Api-Key"]; ok {
				t.Error("x-api-key present")
			}
			return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
		}))
		recorder := &providerEventRecorder{}
		stream, err := provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE, OnProviderStreamEvent: recorder.observe})
		if err != nil {
			t.Fatal(err)
		}
		text, done := false, false
		for event := range stream.Events(t.Context()) {
			switch event := event.(type) {
			case TextDeltaEvent:
				text = true
			case DoneEvent:
				done = true
				if codexUpstreamText(event.Message) != "Hello" {
					t.Fatal(event.Message)
				}
			}
		}
		if !text || !done {
			t.Fatalf("text=%t done=%t", text, done)
		}
		events, models := recorder.snapshot()
		var types []string
		for _, event := range events {
			types = append(types, event.(map[string]any)["type"].(string))
		}
		if want := []string{"response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_item.done", "response.completed"}; !reflect.DeepEqual(types, want) {
			t.Fatalf("events = %v", types)
		}
		assertEventModels(t, models, provider.cfg.ModelMetadata, 5)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:217
	t.Run("processes a terminal SSE event without a trailing blank line", func(t *testing.T) {
		provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
			return codexUpstreamHTTP(strings.TrimSpace(codexUpstreamSSE("completed", nil))), nil
		}))
		stream, err := provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonStop || codexUpstreamText(result) != "Hello" {
			t.Fatal(result)
		}
	})
	for _, tc := range []struct {
		name, session, retention, want string
		line                           int
	}{
		{"sets session-id/x-client-request-id headers and prompt_cache_key when sessionId is provided", "test-session-123", "", "test-session-123", 532},
		{"omits SSE cache affinity when cacheRetention is none", "one-off-summary", "none", "", 637},
		{"clamps prompt_cache_key to OpenAI's 64-character limit", strings.Repeat("x", 67), "", strings.Repeat("x", 64), 688},
		{"clamps Codex session-id header to 64 characters", strings.Repeat("x", 67), "", strings.Repeat("x", 64), 738},
		{"does not set session-id/x-client-request-id headers when sessionId is not provided", "", "", "", 1170},
	} {
		t.Run(tc.name, func(t *testing.T) { // .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:532,637,688,738,1170
			var body map[string]json.RawMessage
			var headers http.Header
			provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(r *http.Request) (*http.Response, error) {
				body = codexUpstreamBody(t, r)
				headers = r.Header.Clone()
				return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
			}))
			stream, err := provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE, SessionID: tc.session, CacheRetention: CacheRetention(tc.retention)})
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.Result()
			for _, key := range []string{"session-id", "x-client-request-id"} {
				if _, present := headers[http.CanonicalHeaderKey(key)]; tc.want == "" && present {
					t.Errorf("unexpected header %s", key)
				}
				if headers.Get(key) != tc.want {
					t.Errorf("%s=%q want=%q", key, headers.Get(key), tc.want)
				}
			}
			if _, present := headers[http.CanonicalHeaderKey("session_id")]; present {
				t.Error("unexpected session_id")
			}
			if tc.want == "" {
				if _, ok := body["prompt_cache_key"]; ok {
					t.Errorf("prompt_cache_key=%s", body["prompt_cache_key"])
				}
			} else {
				assertShapeJSON(t, body["prompt_cache_key"], fmt.Sprintf("%q", tc.want))
			}
		})
	}
	for _, tc := range []struct {
		name, id string
		level    ModelThinkingLevel
		want     string
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:786
		{"preserves gpt-5.5 xhigh reasoning effort from simple options", "gpt-5.5", ThinkingXHigh, "xhigh"},
		// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:971
		{"clamps gpt-5.3-codex minimal reasoning effort to low", "gpt-5.3-codex", ThinkingMinimal, "low"},
		{"clamps gpt-5.4 minimal reasoning effort to low", "gpt-5.4", ThinkingMinimal, "low"},
		{"clamps gpt-5.5 minimal reasoning effort to low", "gpt-5.5", ThinkingMinimal, "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]json.RawMessage
			provider := codexUpstreamProvider(t, tc.id, codexRoundTripper(func(r *http.Request) (*http.Response, error) {
				body = codexUpstreamBody(t, r)
				return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
			}))
			provider.cfg.ModelMetadata.ThinkingLevelMap = ThinkingLevelMap{ModelThinkingLevel(tc.level): new(tc.want)}
			options := StreamOptions{Transport: TransportSSE}
			if tc.level == ThinkingXHigh {
				options.Thinking = tc.level.ReasoningOption()
			} else {
				options.ReasoningEffort = string(tc.level)
			}
			stream, err := provider.Stream(t.Context(), codexUpstreamContext(), options)
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.Result()
			assertShapeJSON(t, body["reasoning"], fmt.Sprintf(`{"effort":%q,"summary":"auto"}`, tc.want))
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:847
	t.Run("forwards required tool choice", func(t *testing.T) {
		ctx := Context{Messages: []Message{UserMessage{Content: UserText("Do not call ping. Respond with text instead.")}}, Tools: []ToolSchema{{Name: "ping", Description: "Ping", Parameters: JsonObject{"type": "object", "properties": JsonObject{"value": JsonObject{"type": "string"}}, "required": []string{"value"}}}}}
		var body map[string]json.RawMessage
		provider := codexUpstreamProvider(t, "gpt-5.5", codexRoundTripper(func(r *http.Request) (*http.Response, error) {
			body = codexUpstreamBody(t, r)
			return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
		}))
		stream, err := provider.Stream(t.Context(), NormalizeContext(ctx), StreamOptions{Transport: TransportSSE, ToolChoice: "required"})
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Result()
		assertShapeJSON(t, body["tool_choice"], `"required"`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:902
	t.Run("sets Codex strict mode explicitly and honors constrained sampling", func(t *testing.T) {
		var tools []ToolSchema
		if err := json.Unmarshal([]byte(`[{"name":"optional","description":"Optional constrained sampling","parameters":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},"constrainedSampling":false},{"name":"strict","description":"Strict constrained sampling","parameters":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false},"constrainedSampling":{"type":"json_schema","strict":"prefer"}}]`), &tools); err != nil {
			t.Fatal(err)
		}
		var body map[string]json.RawMessage
		provider := codexUpstreamProvider(t, "gpt-5.5", codexRoundTripper(func(r *http.Request) (*http.Response, error) {
			body = codexUpstreamBody(t, r)
			return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
		}))
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Use a tool")}}, Tools: tools}), StreamOptions{Transport: TransportSSE})
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Result()
		var got []map[string]json.RawMessage
		if err = json.Unmarshal(body["tools"], &got); err != nil || len(got) != 2 {
			t.Fatalf("tools=%s err=%v", body["tools"], err)
		}
		for i, name := range []string{"optional", "strict"} {
			assertShapeJSON(t, got[i]["name"], fmt.Sprintf("%q", name))
			assertShapeJSON(t, got[i]["type"], `"function"`)
		}
		assertShapeJSON(t, got[0]["strict"], `null`)
		assertShapeJSON(t, got[1]["strict"], `true`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:1071
	for _, tc := range []struct {
		id, tier   string
		multiplier float64
	}{{"gpt-5.1-codex", "flex", 0.5}, {"gpt-5.1-codex", "priority", 2}, {"gpt-5.5", "flex", 0.5}, {"gpt-5.5", "priority", 2.5}} {
		t.Run("uses the client-sent "+tc.id+" service tier for "+tc.tier+" when Codex echoes default", func(t *testing.T) {
			provider := codexUpstreamProvider(t, tc.id, codexRoundTripper(func(*http.Request) (*http.Response, error) {
				return codexUpstreamHTTP(`data: {"type":"response.completed","response":{"status":"completed","service_tier":"default","usage":{"input_tokens":1000000,"output_tokens":1000000,"total_tokens":2000000,"input_tokens_details":{"cached_tokens":0}}}}` + "\n\n"), nil
			}))
			stream, err := provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE, ServiceTier: tc.tier, ModelCost: ModelCost{Input: 1, Output: 2}})
			if err != nil {
				t.Fatal(err)
			}
			cost := stream.Result().Usage.Cost
			if cost.Input != tc.multiplier || cost.Output != 2*tc.multiplier || cost.Total != 3*tc.multiplier {
				t.Fatalf("cost=%#v multiplier=%g", cost, tc.multiplier)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:2519
	t.Run("zstd-compresses SSE request bodies", func(t *testing.T) {
		for _, text := range []string{strings.Repeat("compress me ", 400), "hi"} {
			provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Content-Encoding") != "zstd" {
					t.Error("not zstd")
				}
				body := codexUpstreamBody(t, r)
				var input []struct{ Content []struct{ Text string } }
				if err := json.Unmarshal(body["input"], &input); err != nil || len(input) == 0 || len(input[0].Content) == 0 || input[0].Content[0].Text != text {
					t.Fatalf("input=%s err=%v", body["input"], err)
				}
				return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
			}))
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText(text), Timestamp: 1}}}), StreamOptions{Transport: TransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.Result()
		}
	})
}

func TestCodexSSELifetimeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		stop         StopReason
		end          *bool
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:247
		{"completes after response.completed even when the SSE body stays open", "completed", StopReasonStop, new(false)},
		// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:308
		{"maps response.incomplete to stopReason length even when the SSE body stays open", "incomplete", StopReasonLength, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
				}))
				go func() { _, _ = io.WriteString(writer, codexUpstreamSSE(tc.status, tc.end)) }()
				stream, err := provider.Stream(ctx, codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
				if err != nil {
					t.Fatal(err)
				}
				result := stream.Result()
				if result.StopReason != tc.stop || codexUpstreamText(result) != "Hello" || !reflect.DeepEqual(result.EndTurn, tc.end) {
					t.Fatal(result)
				}
			})
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:368
	t.Run("aborts SSE fetch after the configured HTTP timeout when response headers do not arrive", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				<-r.Context().Done()
				return nil, r.Context().Err()
			}))
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			stream, err := provider.Stream(ctx, codexUpstreamContext(), StreamOptions{Transport: TransportSSE, TimeoutMs: new(10)})
			err = awaitProviderFailure(t, stream, err)
			if calls != 1 || err == nil || err.Error() != "Codex SSE response headers timed out after 10ms" {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	})
	// .upstream/v0.87.1/packages/ai/test/openai-codex-stream.test.ts:424
	t.Run("aborts SSE body reads after response headers arrive", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader, writer := io.Pipe()
			defer writer.Close()
			closed := make(chan struct{})
			body := &codexAbortBody{ReadCloser: reader, closed: closed}
			provider := codexUpstreamProvider(t, "gpt-5.1-codex", codexRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
			}))
			writerDone := make(chan struct{})
			go func() {
				defer close(writerDone)
				_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\ndata: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"one\"}\n\n")
				time.Sleep(10 * time.Millisecond)
				if ctx.Err() == nil {
					_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"two\"}\n\n")
				}
				time.Sleep(10 * time.Millisecond)
				_ = writer.Close()
			}()
			stream, err := provider.Stream(ctx, codexUpstreamContext(), StreamOptions{Transport: TransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			var deltas []string
			for event := range stream.Events(t.Context()) {
				if event, ok := event.(TextDeltaEvent); ok {
					deltas = append(deltas, event.Delta)
					if event.Delta == "one" {
						cancel()
					}
				}
			}
			result := stream.Result()
			if result.StopReason != StopReasonAborted || result.ErrorMessage != "Request was aborted" || !reflect.DeepEqual(deltas, []string{"one"}) {
				t.Fatalf("result=%#v deltas=%v", result, deltas)
			}
			<-closed
			<-writerDone
		})
	})
}

func BenchmarkCodexSSERequest(b *testing.B) {
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestTokenForAccount("benchmark"), Model: "gpt-5.1-codex", ProviderID: "openai-codex"}).(*openAIResponsesProvider)
	body := codexUpstreamSSE("completed", nil)
	provider.client = &http.Client{Transport: codexRoundTripper(func(*http.Request) (*http.Response, error) { return codexUpstreamHTTP(body), nil })}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(strings.Repeat("compress me ", 400))}}})
	b.ReportAllocs()
	for b.Loop() {
		stream, err := provider.Stream(context.Background(), transcript, StreamOptions{Transport: TransportSSE})
		if err != nil {
			b.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonStop {
			b.Fatal(result)
		}
	}
}

type codexAbortBody struct {
	io.ReadCloser
	closed chan struct{}
}

func (body *codexAbortBody) Close() error { close(body.closed); return body.ReadCloser.Close() }
