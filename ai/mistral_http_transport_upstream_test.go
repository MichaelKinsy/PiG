package ai

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"
)

func mistralUpstreamSSE(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// mistralCreateSSEBody is the body of createSseResponse at .upstream/v0.99.1/packages/ai/test/mistral-http-transport.test.ts:10.
func mistralCreateSSEBody(events ...string) string {
	frames := make([]string, len(events))
	for i, event := range events {
		frames[i] = "data: " + event
	}
	return strings.Join(frames, "\r\n\r\n") + "\r\n\r\ndata: [DONE]\r\n\r\n"
}

type mistralObservedResponseBody struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (b *mistralObservedResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { close(b.closed) })
	return err
}

const mistralUpstreamTerminal = "data: {\"id\":\"mistral-response-id\",\"model\":\"mistral-large-latest\",\"choices\":[{\"index\":0,\"finish_reason\":\"stop\",\"delta\":{}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\r\n\r\ndata: [DONE]\r\n\r\n"

func mistralUpstreamProvider(t *testing.T) *mistralProvider {
	t.Helper()
	m := mustGeneratedModel(t, "mistral", "mistral-large-latest")
	return NewMistralProvider(MistralConfig{Model: m.ID, BaseURL: m.BaseURL, APIKey: "test", ExtraHeaders: m.Headers, Reasoning: m.Reasoning}).(*mistralProvider)
}

func mistralUpstreamContext() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}})
}

// packages/ai/src/api/mistral-conversations.ts:689-739 keys fragments by tool index and retains the first block's identity, including when another call's fragments intervene.
func TestMistralInterleavedToolFragmentsKeepFirstIdentityUpstream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"abc123456","function":{"name":"lookup","arguments":"{\"query\":"}},{"index":1,"id":"def123456","function":{"name":"lookup","arguments":"{\"query\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"name":"","arguments":"\"second\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"later-id-is-ignored","function":{"name":"later-name-is-ignored","arguments":"\"first\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`, "",
	}, "\r\n\r\n")
	stream, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
		return mistralUpstreamSSE(body), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	message := stream.Result()
	if message.StopReason != StopReasonToolUse {
		t.Fatalf("result = %#v", message)
	}
	assertCatalogJSON(t, message.Content, `[{"type":"toolCall","id":"abc123456","name":"lookup","arguments":{"query":"first"}},{"type":"toolCall","id":"def123456","name":"lookup","arguments":{"query":"second"}}]`)
}

func TestMistralHTTPTransportUpstream(t *testing.T) {
	// Ports packages/ai/test/mistral-http-transport.test.ts:40.
	t.Run("serializes SDK-style payloads to the Mistral wire format", func(t *testing.T) {
		p := mistralUpstreamProvider(t)
		p.cfg.APIKey = "secret"
		request := NormalizeContext(Context{SystemPrompt: "Be precise", Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "describe"}, ImageContent{Data: "aGVsbG8=", MimeType: "image/png"}}, Timestamp: 1}}, Tools: []ToolSchema{{Name: "lookup", Description: "Look something up", Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}}}})
		var wire, callback map[string]any
		var response ProviderResponse
		opts := StreamOptions{MaxTokens: 123, ReasoningEffort: "high", SessionID: "session-1", Headers: ProviderHeaders{"x-custom": new("value")}, ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}}
		// Decode the upstream raw option at the public boundary so a missing promptMode option is a behavioral failure, not an uncompilable test.
		if err := json.Unmarshal([]byte(`{"promptMode":"reasoning"}`), &opts); err != nil {
			t.Fatal(err)
		}
		opts.OnPayload = func(payload any, _ *Model) (any, error) {
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &callback); err != nil {
				t.Fatal(err)
			}
			var replacement map[string]any
			if err = json.Unmarshal(raw, &replacement); err != nil {
				t.Fatal(err)
			}
			maps.Copy(replacement, map[string]any{"topP": 0.9, "randomSeed": 42, "presencePenalty": 0.1, "frequencyPenalty": 0.2, "parallelToolCalls": true, "safePrompt": true, "responseFormat": map[string]any{"type": "json_schema", "jsonSchema": map[string]any{"name": "result", "schemaDefinition": map[string]any{"type": "object", "properties": map[string]any{"maxTokens": map[string]any{"type": "number"}}}}}})
			return replacement, nil
		}
		opts.OnResponse = func(_ context.Context, r ProviderResponse, _ *Model) error { response = r; return nil }
		opts.Fetch = &http.Client{Transport: FetchFunction(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.mistral.ai/v1/chat/completions" {
				t.Errorf("url=%s", r.URL)
			}
			for key, want := range map[string]string{"authorization": "Bearer secret", "accept": "text/event-stream", "x-affinity": "session-1", "x-custom": "value", "user-agent": PiUserAgent()} {
				if got := r.Header.Get(key); got != want {
					t.Errorf("header %s=%q want %q", key, got, want)
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
				t.Error(err)
			}
			resp := mistralUpstreamSSE(mistralUpstreamTerminal)
			resp.Header.Set("x-request-id", "request-1")
			return resp, nil
		})}
		stream, err := p.Stream(t.Context(), request, opts)
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonStop {
			t.Fatal(result)
		}
		// OnPayload receives SDK-style property names; only the transport serialization lowers them.
		if callback["maxTokens"] != float64(123) || callback["promptMode"] != "reasoning" || callback["promptCacheKey"] != "session-1" {
			t.Errorf("callback payload=%v", callback)
		}
		if response.Status != 200 || !reflect.DeepEqual(response.Headers, map[string]string{"content-type": "text/event-stream", "x-request-id": "request-1"}) {
			t.Errorf("response=%#v", response)
		}
		for key, want := range map[string]any{"max_tokens": float64(123), "prompt_mode": "reasoning", "reasoning_effort": "high", "prompt_cache_key": "session-1", "top_p": 0.9, "random_seed": float64(42), "presence_penalty": 0.1, "frequency_penalty": 0.2, "parallel_tool_calls": true, "safe_prompt": true} {
			if got := wire[key]; got != want {
				t.Errorf("wire %s=%v want %v", key, got, want)
			}
		}
		assertCatalogJSON(t, wire["tool_choice"], `{"type":"function","function":{"name":"lookup"}}`)
		assertCatalogJSON(t, wire["response_format"], `{"type":"json_schema","json_schema":{"name":"result","schema":{"type":"object","properties":{"maxTokens":{"type":"number"}}}}}`)
		for _, key := range []string{"maxTokens", "promptMode", "promptCacheKey"} {
			if _, ok := wire[key]; ok {
				t.Errorf("wire contains %s", key)
			}
		}
		assertCatalogJSON(t, wire["messages"], `[{"role":"system","content":"Be precise"},{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":"data:image/png;base64,aGVsbG8="}]}]`)
	})
	// Ports packages/ai/test/mistral-http-transport.test.ts:161.
	t.Run("serializes assistant thinking, tool calls, and tool results for replay", func(t *testing.T) {
		p := mistralUpstreamProvider(t)
		request := NormalizeContext(Context{Messages: []Message{AssistantMessage{API: APIMistralConversations, Provider: "mistral", Model: p.cfg.Model, Content: []AssistantContentBlock{ThinkingContent{Thinking: "reason"}, TextContent{Text: "answer"}, ToolCall{ID: "abc123456", Name: "lookup", Arguments: JsonObject{"query": "pi"}}}, StopReason: StopReasonToolUse, Timestamp: 1}, ToolResultMessage{ToolCallID: "abc123456", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "found"}, ImageContent{Data: "aGVsbG8=", MimeType: "image/png"}}, Timestamp: 2}}})
		var wire map[string]any
		stream, err := p.Stream(t.Context(), request, StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(r *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
				t.Error(err)
			}
			return mistralUpstreamSSE(mistralUpstreamTerminal), nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonStop {
			t.Fatal(result)
		}
		assertCatalogJSON(t, wire["messages"], `[{"role":"assistant","prefix":false,"content":[{"type":"thinking","thinking":[{"type":"text","text":"reason"}]},{"type":"text","text":"answer"}],"tool_calls":[{"id":"abc123456","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"pi\"}"},"index":0}]},{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"found"},{"type":"image_url","image_url":"data:image/png;base64,aGVsbG8="}]}]`)
	})
	// Ports packages/ai/test/mistral-http-transport.test.ts:238.
	t.Run("parses native thinking, text, fragmented tool calls, and cached-token usage", func(t *testing.T) {
		body := strings.Join([]string{
			`data: {"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":null,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"reason"}]}]}}]}`,
			`data: {"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":null,"delta":{"content":[{"type":"text","text":"answer"}]}}]}`,
			`data: {"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":null,"delta":{"tool_calls":[{"id":"abc123456","index":0,"function":{"name":"lookup","arguments":"{\"query\":"}}]}}]}`,
			`data: {"id":"response-1","model":"mistral-large-latest","choices":[{"index":0,"finish_reason":"tool_calls","delta":{"tool_calls":[{"index":0,"function":{"name":"","arguments":"\"pi\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":3}}}`,
			`data: [DONE]`, ""}, "\r\n\r\n")
		stream, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) { return mistralUpstreamSSE(body), nil })}})
		if err != nil {
			t.Fatal(err)
		}
		m := stream.Result()
		if m.StopReason != StopReasonToolUse || m.RawStopReason != "tool_calls" || m.ResponseID != "response-1" {
			t.Fatalf("message=%#v", m)
		}
		assertCatalogJSON(t, m.Content, `[{"type":"thinking","thinking":"reason"},{"type":"text","text":"answer"},{"type":"toolCall","id":"abc123456","name":"lookup","arguments":{"query":"pi"}}]`)
		if m.Usage.Input != 7 || m.Usage.Output != 4 || m.Usage.CacheRead != 3 || m.Usage.CacheWrite != 0 || m.Usage.TotalTokens != 14 {
			t.Fatal(m.Usage)
		}
	})
	// Ports packages/ai/test/mistral-http-transport.test.ts:325.
	t.Run("parses SSE and UTF-8 sequences split across transport chunks", func(t *testing.T) {
		resp := mistralUpstreamSSE("data: {\"id\":\"response-bytewise\",\"model\":\"mistral-large-latest\",\"choices\":[{\"index\":0,\"finish_reason\":\"stop\",\"delta\":{\"content\":\"héllo 🌍\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\r\n\r\ndata: [DONE]\r\n\r\n")
		resp.Body = io.NopCloser(iotest.OneByteReader(resp.Body))
		stream, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) { return resp, nil })}})
		if err != nil {
			t.Fatal(err)
		}
		m := stream.Result()
		if m.StopReason != StopReasonStop {
			t.Fatal(m)
		}
		assertCatalogJSON(t, m.Content, `[{"type":"text","text":"héllo 🌍"}]`)
	})
	// Ports packages/ai/test/mistral-http-transport.test.ts:344.
	t.Run("honors case-insensitive header overrides and explicit affinity suppression", func(t *testing.T) {
		p := mistralUpstreamProvider(t)
		p.cfg.ExtraHeaders = map[string]string{"Authorization": "Bearer model-key", "X-Affinity": "model-affinity"}
		p.cfg.APIKey = "request-key"
		stream, err := p.Stream(t.Context(), mistralUpstreamContext(), StreamOptions{SessionID: "automatic-affinity", Headers: ProviderHeaders{"authorization": nil, "x-affinity": nil, "User-Agent": new("custom-agent")}, Fetch: &http.Client{Transport: FetchFunction(func(r *http.Request) (*http.Response, error) {
			if _, ok := r.Header["Authorization"]; ok {
				t.Error("authorization not removed")
			}
			if _, ok := r.Header["X-Affinity"]; ok {
				t.Error("affinity not removed")
			}
			if r.Header.Get("User-Agent") != "custom-agent" {
				t.Error(r.Header)
			}
			return mistralUpstreamSSE(mistralUpstreamTerminal), nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		stream.Result()
	})
	// Ports packages/ai/test/mistral-http-transport.test.ts:370,395. Default/zero deadlines additionally exercise requestMistralStream's nullish timeout selection at packages/ai/src/api/mistral-conversations.ts:306.
	for _, tc := range []struct {
		name    string
		abort   bool
		timeout *int
		elapsed time.Duration
	}{
		{name: "aborts while waiting for an SSE chunk", abort: true},
		{name: "applies the request timeout while waiting for an SSE chunk", timeout: new(5), elapsed: 5 * time.Millisecond},
		{name: "uses the default request timeout while waiting for an SSE chunk", elapsed: 60 * time.Second},
		{name: "keeps an explicit zero request timeout armed", timeout: new(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				body := &mistralObservedResponseBody{ReadCloser: reader, closed: make(chan struct{})}
				opts := StreamOptions{TimeoutMs: tc.timeout, Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
					resp := mistralUpstreamSSE("")
					resp.Body = body
					return resp, nil
				})}}
				start := time.Now()
				stream, err := mistralUpstreamProvider(t).Stream(ctx, mistralUpstreamContext(), opts)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan *AssistantMessage, 1)
				go func() { done <- stream.Result() }()
				synctest.Wait()
				if tc.abort {
					cancel()
				} else {
					time.Sleep(tc.elapsed)
				}
				synctest.Wait()
				select {
				case m := <-done:
					if tc.abort {
						if m.StopReason != StopReasonAborted {
							t.Fatal(m)
						}
					} else if m.StopReason != StopReasonError || !strings.Contains(strings.ToLower(m.ErrorMessage), "timeout") {
						t.Fatal(m)
					}
				default:
					t.Fatal("result did not settle while waiting for an SSE chunk")
				}
				if elapsed := time.Since(start); elapsed != tc.elapsed {
					t.Errorf("request settled after %s, want %s", elapsed, tc.elapsed)
				}
				select {
				case <-body.closed:
				default:
					t.Fatal("request settled without closing the stalled response body")
				}
			})
		})
	}
	// Ports packages/ai/test/mistral-http-transport.test.ts:418.
	t.Run("preserves HTTP status and response bodies in errors", func(t *testing.T) {
		stream, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
			r := mistralUpstreamSSE(`{"message":"blocked by gateway"}`)
			r.StatusCode = 403
			r.Status = "403 Forbidden"
			return r, nil
		})}})
		// Direct Go APIs return pre-generation errors instead of manufacturing an event stream.
		if stream != nil || err == nil || err.Error() != `Mistral API error (403): {"message":"blocked by gateway"}` {
			t.Fatalf("stream=%v err=%v", stream, err)
		}
	})
}

// .upstream/v0.99.1/packages/ai/test/mistral-http-transport.test.ts:327 (#9674): GLM models on Mistral send empty content deltas at the start, around tool calls,
// and sometimes mid-thinking. They must not open blocks or split thinking.
func TestMistralIgnoresEmptyContentDeltasUpstream(t *testing.T) {
	model := mustGeneratedModel(t, "mistral", "zai-glm-5-3").ToModel()
	thinking := func(text string) string {
		return `{"type":"thinking","thinking":[{"type":"text","text":` + strconv.Quote(text) + `}]}`
	}
	toolCall := func(args string, first bool) string {
		if first {
			return `{"index":0,"id":"abc123456","function":{"name":"read","arguments":` + strconv.Quote(args) + `}}`
		}
		return `{"index":0,"function":{"name":"","arguments":` + strconv.Quote(args) + `}}`
	}
	deltas := []string{
		`{"content":""}`,
		`{"content":[` + thinking("first part,") + `]}`,
		`{"content":""}`,
		`{"content":[{"type":"text","text":""}]}`,
		`{"content":[` + thinking(" second part.") + `,{"type":"text","text":"Reading."}]}`,
		`{"content":"","tool_calls":[` + toolCall("", true) + `]}`,
		`{"content":"","tool_calls":[` + toolCall(`{"path":`, false) + `]}`,
		`{"content":"","tool_calls":[` + toolCall(`"a.txt"}`, false) + `]}`,
		`{"content":""}`,
	}
	events := make([]string, len(deltas))
	for i, delta := range deltas {
		finish := "null"
		if i == len(deltas)-1 {
			finish = `"tool_calls"`
		}
		events[i] = `{"id":"response-1","model":` + strconv.Quote(model.ID) + `,"choices":[{"index":0,"finish_reason":` + finish + `,"delta":` + delta + `}]}`
	}
	stream, err := StreamSimple(t.Context(), model, mistralUpstreamContext(), StreamOptions{APIKey: "test", Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
		return mistralUpstreamSSE(mistralCreateSSEBody(events...)), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	message := stream.Result()
	want := []AssistantContentBlock{
		ThinkingContent{Thinking: "first part, second part."},
		TextContent{Text: "Reading."},
		ToolCall{ID: "abc123456", Name: "read", Arguments: JsonObject{"path": "a.txt"}},
	}
	if !reflect.DeepEqual(message.Content, want) {
		t.Fatalf("stopReason=%q content=%#v error=%q", message.StopReason, message.Content, message.ErrorMessage)
	}
}
