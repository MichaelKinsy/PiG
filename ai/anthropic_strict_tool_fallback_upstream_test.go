package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Ports .upstream/v1.2.0/packages/ai/test/anthropic-strict-tool-fallback.test.ts ("Anthropic preferred strict tool fallback", Pi #10409).

const strictFallbackGrammarError = "The compiled grammar is too large, which would cause performance issues. Simplify your tool schemas or reduce the number of strict tools."

func strictFallbackTool(t *testing.T, name, strict string) ToolSchema {
	t.Helper()
	document := `{"name":"` + name + `","description":"Look up a value","parameters":{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"]}`
	if strict != "" {
		document += `,"constrainedSampling":{"type":"json_schema","strict":"` + strict + `"}`
	}
	var tool ToolSchema
	if err := json.Unmarshal([]byte(document+"}"), &tool); err != nil {
		t.Fatal(err)
	}
	return tool
}

func strictFallbackErrorBody(message, errorType string) string {
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": errorType, "message": message}})
	return string(body)
}

func strictFallbackSuccess() string {
	return anthropicFixtureEvent("message_start", `{"message":{"id":"msg_test","model":"claude-haiku-4-5","usage":{"input_tokens":10,"output_tokens":0}}}`) +
		anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`) +
		anthropicFixtureEvent("message_stop", `{}`)
}

type strictFallbackReply struct {
	status int
	body   string
}

// strictFallbackHarness serves each request with respond and records the request bodies, as the test's harness() records the fetched payloads.
type strictFallbackHarness struct {
	mu       sync.Mutex
	payloads []map[string]any
	server   *httptest.Server
}

func newStrictFallbackHarness(t *testing.T, respond func(attempt int) strictFallbackReply) *strictFallbackHarness {
	t.Helper()
	h := &strictFallbackHarness{}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		h.mu.Lock()
		h.payloads = append(h.payloads, payload)
		attempt := len(h.payloads)
		h.mu.Unlock()
		reply := respond(attempt)
		if reply.status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(reply.status)
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		_, _ = io.WriteString(w, reply.body)
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *strictFallbackHarness) provider(apiKey string, compat ModelCompat) Provider {
	compat.SupportsStrictTools = new(true)
	return NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", ProviderID: "anthropic", BaseURL: h.server.URL, APIKey: apiKey, Compat: &compat})
}

func (h *strictFallbackHarness) requests() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.payloads...)
}

func strictFallbackTools(payload map[string]any) []map[string]any {
	var tools []map[string]any
	for _, tool := range payload["tools"].([]any) {
		tools = append(tools, tool.(map[string]any))
	}
	return tools
}

func strictFallbackRun(t *testing.T, provider Provider, transcript Context, opts StreamOptions) (*AssistantMessage, []string) {
	t.Helper()
	zero := 0
	if opts.MaxRetries == nil {
		opts.MaxRetries = &zero
	}
	stream, err := provider.Stream(t.Context(), NormalizeContext(transcript), opts)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for event := range stream.Events(t.Context()) {
		events = append(events, string(event.EventType()))
	}
	return stream.Result(), events
}

func strictFallbackContext(tools ...ToolSchema) Context {
	return Context{Tools: tools, Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}
}

func TestAnthropicPreferredStrictToolFallbackUpstream(t *testing.T) {
	for _, message := range []string{
		strictFallbackGrammarError,
		"Too many strict tools (32). The maximum number of strict tools supported is 20. Try reducing the number of tools marked as strict.",
		"Schemas contains too many parameters with union types (480 parameters with type arrays or anyOf). This causes exponential compilation cost. Reduce the number of nullable or union-typed parameters (limit: 16 parameters with unions).",
	} {
		t.Run("retries "+message[:24]+" with original non-strict schemas and preserves required tools", func(t *testing.T) {
			h := newStrictFallbackHarness(t, func(attempt int) strictFallbackReply {
				if attempt == 1 {
					return strictFallbackReply{http.StatusBadRequest, strictFallbackErrorBody(message, "invalid_request_error")}
				}
				return strictFallbackReply{http.StatusOK, strictFallbackSuccess()}
			})
			payloadHooks, responses := 0, 0
			result, events := strictFallbackRun(t, h.provider("test-key", ModelCompat{}), strictFallbackContext(strictFallbackTool(t, "preferred", "prefer"), strictFallbackTool(t, "required", "require"), strictFallbackTool(t, "ordinary", "")), StreamOptions{
				CacheRetention: CacheRetentionLong,
				OnPayload: func(payload any, _ *Model) (any, error) {
					payloadHooks++
					data, _ := json.Marshal(payload)
					var params map[string]any
					_ = json.Unmarshal(data, &params)
					params["inference_geo"], params["stream"] = "us", false
					return params, nil
				},
				OnResponse: func(context.Context, ProviderResponse, *Model) error { responses++; return nil },
			})
			if result.StopReason != StopReasonStop {
				t.Fatalf("stop = %s (%s)", result.StopReason, result.ErrorMessage)
			}
			if !reflect.DeepEqual(events, []string{"start", "done"}) {
				t.Fatalf("events = %v", events)
			}
			payloads := h.requests()
			if len(payloads) != 2 || payloadHooks != 2 || responses != 1 {
				t.Fatalf("payloads = %d, onPayload = %d, onResponse = %d", len(payloads), payloadHooks, responses)
			}
			first, second := strictFallbackTools(payloads[0]), strictFallbackTools(payloads[1])
			schema := first[0]["input_schema"].(map[string]any)
			optional := schema["properties"].(map[string]any)["optional"]
			if first[0]["strict"] != true || !reflect.DeepEqual(schema["required"], []any{"value", "optional"}) ||
				!reflect.DeepEqual(optional, map[string]any{"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}}}) {
				t.Fatalf("first preferred tool = %v", first[0])
			}
			if _, ok := second[0]["strict"]; ok {
				t.Fatalf("second preferred tool keeps strict: %v", second[0])
			}
			wantLegacy := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}, "optional": map[string]any{"type": "number"}}, "required": []any{"value"}}
			if !reflect.DeepEqual(second[0]["input_schema"], wantLegacy) {
				t.Fatalf("second preferred input_schema = %v", second[0]["input_schema"])
			}
			if !reflect.DeepEqual(second[1], first[1]) || second[1]["strict"] != true {
				t.Fatalf("required tool changed: %v -> %v", first[1], second[1])
			}
			if !reflect.DeepEqual(second[2], first[2]) || !reflect.DeepEqual(second[2]["cache_control"], map[string]any{"type": "ephemeral", "ttl": "1h"}) {
				t.Fatalf("ordinary tool changed: %v -> %v", first[2], second[2])
			}
			for i, payload := range payloads {
				if payload["stream"] != true {
					t.Fatalf("payload %d stream = %v", i, payload["stream"])
				}
			}
			if payloads[1]["inference_geo"] != "us" {
				t.Fatalf("second payload misses the hook's inference_geo: %v", payloads[1])
			}
			if len(result.Diagnostics) == 0 || result.Diagnostics[0].Type != "anthropic_strict_tools_fallback" || !reflect.DeepEqual(result.Diagnostics[0].Details, map[string]any{"reason": "strict_tool_limits_exceeded"}) {
				t.Fatalf("diagnostics = %+v", result.Diagnostics)
			}
		})
	}

	t.Run("downgrades inline additions and same-name redefinitions without downgrading require", func(t *testing.T) {
		h := newStrictFallbackHarness(t, func(attempt int) strictFallbackReply {
			if attempt == 1 {
				return strictFallbackReply{http.StatusBadRequest, strictFallbackErrorBody(strictFallbackGrammarError, "invalid_request_error")}
			}
			return strictFallbackReply{http.StatusOK, strictFallbackSuccess()}
		})
		transcript := strictFallbackContext(strictFallbackTool(t, "lookup", "require"))
		transcript.Messages = append(transcript.Messages,
			SystemMessage{ToolsAdded: []ToolSchema{strictFallbackTool(t, "lookup", "prefer")}, Timestamp: 2},
			SystemMessage{ToolsAdded: []ToolSchema{strictFallbackTool(t, "lookup", "require")}, Timestamp: 3},
		)
		result, _ := strictFallbackRun(t, h.provider("sk-ant-oat-test", ModelCompat{SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolChanges: new(true)}), transcript, StreamOptions{})
		if result.StopReason != StopReasonStop {
			t.Fatalf("stop = %s (%s)", result.StopReason, result.ErrorMessage)
		}
		payloads := h.requests()
		if len(payloads) != 2 || !reflect.DeepEqual(payloads[1]["tools"], payloads[0]["tools"]) {
			t.Fatalf("payloads = %d, tools %v -> %v", len(payloads), payloads[0]["tools"], payloads[len(payloads)-1]["tools"])
		}
		additions := make([][]map[string]any, 2)
		for i, payload := range payloads {
			for _, message := range payload["messages"].([]any) {
				content, ok := message.(map[string]any)["content"].([]any)
				if !ok {
					continue
				}
				for _, block := range content {
					block := block.(map[string]any)
					if block["type"] != "tool_addition" {
						continue
					}
					if tool := block["tool"].(map[string]any); tool["type"] == "tool_definition" {
						additions[i] = append(additions[i], tool["definition"].(map[string]any))
					}
				}
			}
		}
		strictOf := func(definitions []map[string]any) []any {
			out := make([]any, len(definitions))
			for i, definition := range definitions {
				out[i] = definition["strict"]
			}
			return out
		}
		if !reflect.DeepEqual(strictOf(additions[0]), []any{true, true}) || !reflect.DeepEqual(strictOf(additions[1]), []any{nil, true}) {
			t.Fatalf("addition strict = %v then %v", strictOf(additions[0]), strictOf(additions[1]))
		}
		if !reflect.DeepEqual(additions[1][0]["input_schema"].(map[string]any)["required"], []any{"value"}) || !reflect.DeepEqual(additions[1][1], additions[0][1]) {
			t.Fatalf("additions = %v then %v", additions[0], additions[1])
		}
	})

	unsupported := strictFallbackTool(t, "lookup", "prefer")
	unsupported.Parameters = map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "number", "minimum": 1}}, "required": []any{"value"}}
	for _, tc := range []struct {
		name  string
		tools []ToolSchema
	}{
		{"required", []ToolSchema{strictFallbackTool(t, "lookup", "require")}},
		{"ordinary", []ToolSchema{strictFallbackTool(t, "lookup", "")}},
		{"locally unsupported prefer", []ToolSchema{unsupported}},
	} {
		t.Run("does not retry a "+tc.name+" tool set", func(t *testing.T) {
			h := newStrictFallbackHarness(t, func(int) strictFallbackReply {
				return strictFallbackReply{http.StatusBadRequest, strictFallbackErrorBody(strictFallbackGrammarError, "invalid_request_error")}
			})
			result, _ := strictFallbackRun(t, h.provider("test-key", ModelCompat{}), strictFallbackContext(tc.tools...), StreamOptions{})
			if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, strictFallbackGrammarError) || len(h.requests()) != 1 {
				t.Fatalf("stop = %s, error = %q, payloads = %d", result.StopReason, result.ErrorMessage, len(h.requests()))
			}
		})
	}

	for _, tc := range []struct {
		name, message, errorType string
		status                   int
	}{
		{"unrelated 400", "Invalid tool schema", "invalid_request_error", http.StatusBadRequest},
		{"wrong error type", strictFallbackGrammarError, "authentication_error", http.StatusBadRequest},
		{"wrong HTTP status", strictFallbackGrammarError, "invalid_request_error", http.StatusInternalServerError},
	} {
		t.Run("does not downgrade on "+tc.name, func(t *testing.T) {
			h := newStrictFallbackHarness(t, func(int) strictFallbackReply {
				return strictFallbackReply{tc.status, strictFallbackErrorBody(tc.message, tc.errorType)}
			})
			result, _ := strictFallbackRun(t, h.provider("test-key", ModelCompat{}), strictFallbackContext(strictFallbackTool(t, "lookup", "prefer")), StreamOptions{})
			if result.StopReason != StopReasonError || len(h.requests()) != 1 {
				t.Fatalf("stop = %s, payloads = %d", result.StopReason, len(h.requests()))
			}
		})
	}

	t.Run("surfaces a second rejection rather than retrying again", func(t *testing.T) {
		h := newStrictFallbackHarness(t, func(attempt int) strictFallbackReply {
			message := strictFallbackGrammarError
			if attempt > 1 {
				message = "Required grammar is still too large"
			}
			return strictFallbackReply{http.StatusBadRequest, strictFallbackErrorBody(message, "invalid_request_error")}
		})
		result, _ := strictFallbackRun(t, h.provider("test-key", ModelCompat{}), strictFallbackContext(strictFallbackTool(t, "lookup", "prefer"), strictFallbackTool(t, "required", "require")), StreamOptions{})
		payloads := h.requests()
		if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "Required grammar is still too large") || len(payloads) != 2 {
			t.Fatalf("stop = %s, error = %q, payloads = %d", result.StopReason, result.ErrorMessage, len(payloads))
		}
		if strictFallbackTools(payloads[1])[1]["strict"] != true {
			t.Fatalf("the required tool lost strict: %v", payloads[1]["tools"])
		}
	})

	t.Run("does not downgrade a hook exception, even if it looks like the provider rejection", func(t *testing.T) {
		h := newStrictFallbackHarness(t, func(int) strictFallbackReply { return strictFallbackReply{http.StatusOK, strictFallbackSuccess()} })
		result, _ := strictFallbackRun(t, h.provider("test-key", ModelCompat{}), strictFallbackContext(strictFallbackTool(t, "lookup", "prefer")), StreamOptions{
			OnPayload: func(any, *Model) (any, error) {
				return nil, anthHTTPError(http.StatusBadRequest, []byte(strictFallbackErrorBody(strictFallbackGrammarError, "invalid_request_error")))
			},
		})
		if result.StopReason != StopReasonError || len(h.requests()) != 0 || result.Diagnostics != nil {
			t.Fatalf("stop = %s, payloads = %d, diagnostics = %+v", result.StopReason, len(h.requests()), result.Diagnostics)
		}
	})

	t.Run("honors cancellation before the fallback attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		h := newStrictFallbackHarness(t, func(int) strictFallbackReply {
			cancel()
			return strictFallbackReply{http.StatusBadRequest, strictFallbackErrorBody(strictFallbackGrammarError, "invalid_request_error")}
		})
		zero := 0
		stream, err := h.provider("test-key", ModelCompat{}).Stream(ctx, NormalizeContext(strictFallbackContext(strictFallbackTool(t, "lookup", "prefer"))), StreamOptions{MaxRetries: &zero})
		if err != nil {
			t.Fatal(err)
		}
		result, err := stream.ResultContext(t.Context())
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if result == nil || result.StopReason != StopReasonAborted || len(h.requests()) != 1 {
			t.Fatalf("result = %+v, payloads = %d", result, len(h.requests()))
		}
	})

	t.Run("does not retry an SSE error after response headers", func(t *testing.T) {
		h := newStrictFallbackHarness(t, func(int) strictFallbackReply {
			return strictFallbackReply{http.StatusOK, "event: error\ndata: " + strictFallbackErrorBody(strictFallbackGrammarError, "invalid_request_error") + "\n\n"}
		})
		result, _ := strictFallbackRun(t, h.provider("test-key", ModelCompat{}), strictFallbackContext(strictFallbackTool(t, "lookup", "prefer")), StreamOptions{})
		if result.StopReason != StopReasonError || len(h.requests()) != 1 {
			t.Fatalf("stop = %s, payloads = %d", result.StopReason, len(h.requests()))
		}
	})

	t.Run("does not disable strict sampling on later requests", func(t *testing.T) {
		h := newStrictFallbackHarness(t, func(attempt int) strictFallbackReply {
			if attempt == 1 {
				return strictFallbackReply{http.StatusBadRequest, strictFallbackErrorBody(strictFallbackGrammarError, "invalid_request_error")}
			}
			return strictFallbackReply{http.StatusOK, strictFallbackSuccess()}
		})
		provider := h.provider("test-key", ModelCompat{})
		transcript := strictFallbackContext(strictFallbackTool(t, "lookup", "prefer"))
		strictFallbackRun(t, provider, transcript, StreamOptions{})
		strictFallbackRun(t, provider, transcript, StreamOptions{})
		var strict []any
		for _, payload := range h.requests() {
			strict = append(strict, strictFallbackTools(payload)[0]["strict"])
		}
		if !reflect.DeepEqual(strict, []any{true, nil, true}) {
			t.Fatalf("strict per request = %v", strict)
		}
	})

	t.Run("preserves prefer through pi-messages so the gateway's Anthropic adapter can fall back", func(t *testing.T) {
		h := newStrictFallbackHarness(t, func(attempt int) strictFallbackReply {
			if attempt == 1 {
				return strictFallbackReply{http.StatusBadRequest, strictFallbackErrorBody(strictFallbackGrammarError, "invalid_request_error")}
			}
			return strictFallbackReply{http.StatusOK, strictFallbackSuccess()}
		})
		transcript := strictFallbackContext(strictFallbackTool(t, "lookup", "prefer"))
		want, err := json.Marshal(NormalizeContext(transcript).borrowed())
		if err != nil {
			t.Fatal(err)
		}
		upstream := h.provider("test-key", ModelCompat{})
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Context struct {
					Messages json.RawMessage `json:"messages"`
				} `json:"context"`
			}
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Errorf("gateway request %s: %v", raw, err)
			}
			var got, expected any
			_ = json.Unmarshal(request.Context.Messages, &got)
			_ = json.Unmarshal(want, &expected)
			if !reflect.DeepEqual(got, expected) {
				t.Errorf("gateway context = %s, want %s", request.Context.Messages, want)
			}
			// The gateway's Anthropic adapter runs on the context the request carried, which equals the transcript.
			result, _ := strictFallbackRun(t, upstream, transcript, StreamOptions{})
			usage, _ := json.Marshal(result.Usage)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"type":"done","reason":"`+string(result.StopReason)+`","usage":`+string(usage)+"}\n\n")
		}))
		defer gateway.Close()
		provider := NewPiMessagesProvider(PiMessagesConfig{BaseURL: gateway.URL + "/v1", APIKey: "radius-test", Model: "claude-haiku-4-5", ProviderID: "radius"})
		defer func() { _ = provider.Close() }()
		stream, err := provider.Stream(t.Context(), NormalizeContext(transcript), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonStop {
			t.Fatalf("stop = %s (%s)", result.StopReason, result.ErrorMessage)
		}
		var strict []any
		for _, payload := range h.requests() {
			strict = append(strict, strictFallbackTools(payload)[0]["strict"])
		}
		if !reflect.DeepEqual(strict, []any{true, nil}) {
			t.Fatalf("strict per request = %v", strict)
		}
	})
}
