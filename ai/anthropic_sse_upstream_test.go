package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func anthropicFixtureEvent(kind, data string) string {
	// Upstream fixtures carry the event discriminator in both the SSE field and the JSON object. Do not parse or repair the intentionally malformed tool delta here.
	if strings.HasPrefix(data, "{") && !strings.HasPrefix(data, `{"type":`) {
		if data == "{}" {
			data = fmt.Sprintf(`{"type":%q}`, kind)
		} else {
			data = fmt.Sprintf(`{"type":%q,%s`, kind, data[1:])
		}
	}
	return "event: " + kind + "\ndata: " + data + "\n\n"
}

func anthropicFixtureEnd(reason string) string {
	return anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"`+reason+`"},"usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`) + anthropicFixtureEvent("message_stop", `{}`)
}

func anthropicMinimalFixture() string {
	return anthropicFixtureEvent("message_start", `{"message":{"id":"msg_test","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"text","text":""}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"text_delta","text":"Hello"}}`) +
		anthropicFixtureEvent("content_block_stop", `{"index":0}`) + anthropicFixtureEnd("end_turn")
}

func streamAnthropicFixture(t *testing.T, config AnthropicConfig, events string, opts StreamOptions, contexts ...Context) (*AssistantMessage, map[string]json.RawMessage, http.Header) {
	t.Helper()
	type request struct {
		body    map[string]json.RawMessage
		headers http.Header
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- request{body, r.Header.Clone()}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, events)
	}))
	defer server.Close()
	config.BaseURL = server.URL
	config.APIKey = "fake-key"
	provider := NewAnthropicProvider(config)
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	input := Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}
	if len(contexts) > 0 {
		input = contexts[0]
	}
	stream, err := provider.Stream(t.Context(), NormalizeContext(input), opts)
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	requestValue := <-requests
	return result, requestValue.body, requestValue.headers
}

func BenchmarkAnthropicSSEInitialBlocks(b *testing.B) {
	provider := &anthropicProvider{cfg: AnthropicConfig{Model: "claude-haiku-4-5", ProviderID: "anthropic"}}
	var events strings.Builder
	events.WriteString(anthropicFixtureEvent("message_start", `{"message":{"id":"msg_bench","usage":{"input_tokens":1024,"output_tokens":0}}}`))
	events.WriteString(anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"text","text":"initial"}}`))
	for range 128 {
		events.WriteString(anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"text_delta","text":"streamed text chunk"}}`))
	}
	events.WriteString(anthropicFixtureEvent("content_block_stop", `{"index":0}`))
	events.WriteString(anthropicFixtureEnd("end_turn"))
	input := events.String()
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	for b.Loop() {
		builder := newAssistantStreamBuilder(b.Context(), APIAnthropicMessages, "anthropic", "claude-haiku-4-5")
		provider.parseAnthropicSSE(b.Context(), strings.NewReader(input), builder, anthropicStreamNames{})
		if result := builder.stream.Result(); result.StopReason != StopReasonStop {
			b.Fatal(result.ErrorMessage)
		}
	}
}

func TestAnthropicUpstreamSSEParsing(t *testing.T) {
	responseModelFixture := func(model, content string) string {
		return anthropicFixtureEvent("message_start", fmt.Sprintf(`{"message":{"id":"msg_response_model","model":%q,"usage":{"input_tokens":100,"output_tokens":0}}}`, model)) +
			anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":`+content+`}`) + anthropicFixtureEvent("content_block_stop", `{"index":0}`) +
			anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"output_tokens":20}}`) + anthropicFixtureEvent("message_stop", `{}`)
	}
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:113
	t.Run("keeps signed thinking replayable when a proxy relabels the model", func(t *testing.T) {
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-opus-5"}, responseModelFixture("kimi-for-coding", `{"type":"thinking","thinking":"reasoning","signature":"signature"}`), StreamOptions{})
		if result.Model != "claude-opus-5" || result.ResponseModel != "kimi-for-coding" {
			t.Fatalf("identity=%+v", result)
		}
		want := []AssistantContentBlock{ThinkingContent{Thinking: "reasoning", ThinkingSignature: "signature"}}
		if !reflect.DeepEqual(result.Content, want) {
			t.Fatalf("content=%#v", result.Content)
		}
		contentJSON, err := json.Marshal(result.Content)
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, contentJSON, `[{"type":"thinking","thinking":"reasoning","thinkingSignature":"signature"}]`)
		transformed := TransformMessages([]Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}, *result}, upstreamCatalogModel(t, "anthropic", "claude-opus-5"), nil)
		if !reflect.DeepEqual(transformed[1].(AssistantMessage).Content, want) {
			t.Fatalf("transformed thinking=%#v", transformed[1])
		}
		replay := anthConvertMessages(transformed[1:], false, false, false)
		encoded, err := json.Marshal(replay)
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, `[{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning","signature":"signature"}]}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:140
	t.Run("uses a returned fallback model for cost attribution", func(t *testing.T) {
		config := AnthropicConfig{Model: "claude-opus-5", Compat: &AnthropicMessagesCompat{AllowedFallbackModels: []AnthropicAllowedFallbackModel{{Provider: "anthropic", Model: "fallback-model", Cost: ModelCost{Input: 3, Output: 5}}}}}
		result, _, _ := streamAnthropicFixture(t, config, responseModelFixture("fallback-model", `{"type":"text","text":"done"}`), StreamOptions{})
		// Vitest precision 10 permits a difference strictly below 0.5e-10; negating the comparison also rejects NaN.
		if result.Model != "claude-opus-5" || result.ResponseModel != "fallback-model" || !(math.Abs(result.Usage.Cost.Input-.0003) < 0.5e-10) || !(math.Abs(result.Usage.Cost.Output-.0001) < 0.5e-10) {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:170
	t.Run("fails safely when Anthropic falls back after output begins", func(t *testing.T) {
		events := anthropicFixtureEvent("message_start", `{"message":{"id":"msg_fallback","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":0}}}`) +
			anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"text","text":"partial"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":0}`) +
			anthropicFixtureEvent("content_block_start", `{"index":1,"content_block":{"type":"fallback","from":{"model":"claude-opus-5"},"to":{"model":"claude-opus-4-8"}}}`)
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-opus-5"}, events, StreamOptions{})
		if result.StopReason != StopReasonError || result.ErrorMessage != "Anthropic performed an unsupported mid-output model fallback" {
			t.Fatalf("result=%+v", result)
		}
		contentJSON, err := json.Marshal(result.Content)
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, contentJSON, `[{"type":"text","text":"partial"}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:217
	t.Run("forces streaming after an onPayload replacement", func(t *testing.T) {
		result, body, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-fable-5-1"}, anthropicMinimalFixture(), StreamOptions{OnPayload: func(value any, _ *Model) (any, error) {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				return nil, err
			}
			payload["stream"] = false
			return payload, nil
		}})
		if result.StopReason != StopReasonStop || result.ErrorMessage != "" {
			t.Fatalf("replacement stream failed: %+v", result)
		}
		assertShapeJSON(t, body["stream"], "true")
	})
	// .upstream/v0.99.1/packages/ai/test/anthropic-sse-parsing.test.ts:271 (0.99.1 moved the fixture from anthropic/claude-3-haiku to anthropic/claude-haiku-4.5)
	t.Run("omits the interleaved-thinking beta when thinking is disabled", func(t *testing.T) {
		result, _, headers := streamAnthropicFixture(t, AnthropicConfig{Model: "anthropic/claude-haiku-4.5", ProviderID: "openrouter"}, anthropicMinimalFixture(), StreamOptions{ThinkingEnabled: new(false)})
		if result.StopReason != StopReasonStop || result.ErrorMessage != "" {
			t.Fatalf("disabled-thinking stream failed: %+v", result)
		}
		if headerListContains(headers.Get("Anthropic-Beta"), "interleaved-thinking-2025-05-14") {
			t.Fatal("unexpected interleaved beta")
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:264
	// Go has no SDK client object: upstream's params.betas reach the wire only as the anthropic-beta header, so both managed betas are asserted there.
	t.Run("passes managed beta features to HTTP transport", func(t *testing.T) {
		result, _, headers := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-fable-5-1"}, anthropicMinimalFixture(), StreamOptions{})
		if result.StopReason != StopReasonStop || result.ErrorMessage != "" {
			t.Fatalf("result=%+v", result)
		}
		for _, beta := range []string{"mid-conversation-output-config-2026-07-01", "thinking-binding-controls-2026-08-01"} {
			if !headerListContains(headers.Get("Anthropic-Beta"), beta) {
				t.Errorf("missing beta %s", beta)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:288
	t.Run("uses the serving model input transformations from the final stream event", func(t *testing.T) {
		events := strings.Replace(anthropicMinimalFixture(), anthropicFixtureEvent("message_start", `{"message":{"id":"msg_test","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`), anthropicFixtureEvent("message_start", `{"message":{"id":"msg_transformations","model":"claude-fable-5-1","usage":{"input_tokens":12,"output_tokens":0},"input_transformations":[{"type":"thinking_dropped","path":"messages.1.content.0","reason":"prefix_binding_mismatch"}]}}`), 1)
		events = strings.Replace(events, anthropicFixtureEnd("end_turn"), anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"input_transformations":[{"type":"thinking_dropped","path":"messages.3.content.0","reason":"model_binding_mismatch"}]}`)+anthropicFixtureEvent("message_stop", `{}`), 1)
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-fable-5-1"}, events, StreamOptions{})
		if len(result.Diagnostics) != 1 {
			t.Fatalf("diagnostics=%+v", result.Diagnostics)
		}
		diagnostic := result.Diagnostics[0]
		encoded, err := json.Marshal(diagnostic)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if _, ok := got["timestamp"]; !ok {
			t.Fatal("missing timestamp")
		}
		var timestamp float64
		if err := json.Unmarshal(got["timestamp"], &timestamp); err != nil {
			t.Fatalf("timestamp must be numeric: %v", err)
		}
		delete(got, "timestamp")
		encoded, err = json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, `{"type":"anthropic_input_transformations","details":{"transformations":[{"type":"thinking_dropped","path":"messages.3.content.0","reason":"model_binding_mismatch"}]}}`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:329
	t.Run("repairs malformed SSE JSON and malformed streamed tool JSON", func(t *testing.T) {
		events := anthropicFixtureEvent("message_start", `{"message":{"id":"msg_test","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`) +
			anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"tool_use","id":"toolu_test","name":"edit","input":{}}}`) +
			anthropicFixtureEvent("content_block_delta", "{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"A\\H\\\",\\\"text\\\":\\\"col1\tcol2\\\"}\"}}") +
			anthropicFixtureEvent("content_block_stop", `{"index":0}`) + anthropicFixtureEnd("tool_use")
		input := Context{Messages: []Message{UserMessage{Content: UserText("Use the edit tool."), Timestamp: time.Now().UnixMilli()}}, Tools: []ToolSchema{{Name: "edit", Description: "Edit a file.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}, "required": []string{"path", "text"}}}}}
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, events, StreamOptions{}, input)
		if result.StopReason != StopReasonToolUse || result.ErrorMessage != "" || len(result.Content) != 1 {
			t.Fatalf("result=%+v", result)
		}
		call, ok := result.Content[0].(ToolCall)
		if !ok || !reflect.DeepEqual(call.Arguments, JsonObject{"path": "A\\H", "text": "col1\tcol2"}) {
			t.Fatalf("call=%#v", result.Content[0])
		}
		assertAnthropicSSEContentJSON(t, result, `[{"type":"toolCall","id":"toolu_test","name":"edit","arguments":{"path":"A\\H","text":"col1\tcol2"}}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:416
	t.Run("preserves content from content_block_start events", func(t *testing.T) {
		events := anthropicFixtureEvent("message_start", `{"message":{"id":"msg_initial_content","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`) +
			anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"text","text":"Initial text"}}`) + anthropicFixtureEvent("content_block_delta", `{"index":0,"delta":{"type":"text_delta","text":" plus delta"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":0}`) +
			anthropicFixtureEvent("content_block_start", `{"index":1,"content_block":{"type":"thinking","thinking":"Initial thinking","signature":"initial signature"}}`) + anthropicFixtureEvent("content_block_delta", `{"index":1,"delta":{"type":"thinking_delta","thinking":" plus delta"}}`) + anthropicFixtureEvent("content_block_delta", `{"index":1,"delta":{"type":"signature_delta","signature":" plus delta"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":1}`) + anthropicFixtureEnd("end_turn")
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, events, StreamOptions{}, Context{Messages: []Message{UserMessage{Content: UserText("Say hello."), Timestamp: time.Now().UnixMilli()}}})
		want := []AssistantContentBlock{TextContent{Text: "Initial text plus delta"}, ThinkingContent{Thinking: "Initial thinking plus delta", ThinkingSignature: "initial signature plus delta"}}
		if !reflect.DeepEqual(result.Content, want) {
			t.Fatalf("content=%#v, want %#v", result.Content, want)
		}
		assertAnthropicSSEContentJSON(t, result, `[{"type":"text","text":"Initial text plus delta"},{"type":"thinking","thinking":"Initial thinking plus delta","thinkingSignature":"initial signature plus delta"}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:514
	t.Run("preserves refusal stop details from message_delta", func(t *testing.T) {
		explanation := "This request triggered restrictions on violative cyber content and was blocked under Anthropic's Usage Policy. To learn more, provide feedback, or request an exemption based on how you use Claude, visit our help center: https://support.claude.com/en/articles/14604842-real-time-cyber-safeguards-on-claude."
		events := anthropicFixtureEvent("message_start", `{"message":{"id":"msg_01XFUDYJgAACzvnptvVoYEL","usage":{"input_tokens":412,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`) + anthropicFixtureEvent("message_delta", fmt.Sprintf(`{"delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":%q}},"usage":{"input_tokens":412,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`, explanation)) + anthropicFixtureEvent("message_stop", `{}`)
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-fable-5"}, events, StreamOptions{}, Context{Messages: []Message{UserMessage{Content: UserText("blocked request"), Timestamp: time.Now().UnixMilli()}}})
		if result.StopReason != StopReasonError || result.RawStopReason != "refusal" || result.ErrorMessage != explanation {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:573
	t.Run("preserves sensitive stop reasons with a descriptive error message", func(t *testing.T) {
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, anthropicFixtureEvent("message_start", `{"message":{"id":"msg_sensitive","usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`)+anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"sensitive"},"usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`)+anthropicFixtureEvent("message_stop", `{}`), StreamOptions{}, Context{Messages: []Message{UserMessage{Content: UserText("blocked request"), Timestamp: time.Now().UnixMilli()}}})
		if result.StopReason != StopReasonError || result.RawStopReason != "sensitive" || result.ErrorMessage != "Provider stopped with: sensitive" {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:623
	t.Run("treats message_delta without usage as a no-op for usage accumulation", func(t *testing.T) {
		events := strings.Replace(anthropicMinimalFixture(), anthropicFixtureEnd("end_turn"), anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"end_turn"}}`)+anthropicFixtureEvent("message_stop", `{}`), 1)
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, events, StreamOptions{}, Context{Messages: []Message{UserMessage{Content: UserText("Say hello."), Timestamp: time.Now().UnixMilli()}}})
		if result.StopReason != StopReasonStop || result.ErrorMessage != "" || result.Usage.Input != 12 || result.Usage.TotalTokens != 12 || !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: "Hello"}}) {
			t.Fatalf("result=%+v", result)
		}
		assertAnthropicSSEContentJSON(t, result, `[{"type":"text","text":"Hello"}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-sse-parsing.test.ts:651
	t.Run("ignores unknown SSE events after message_stop", func(t *testing.T) {
		result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, anthropicMinimalFixture()+anthropicFixtureEvent("done", "[DONE]")+anthropicFixtureEvent("proxy.stats", "not json"), StreamOptions{}, Context{Messages: []Message{UserMessage{Content: UserText("Say hello."), Timestamp: time.Now().UnixMilli()}}})
		if result.StopReason != StopReasonStop || result.ErrorMessage != "" || !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: "Hello"}}) {
			t.Fatalf("result=%+v", result)
		}
		assertAnthropicSSEContentJSON(t, result, `[{"type":"text","text":"Hello"}]`)
	})
}

// Serialized blocks also retain Pi's discriminators/signatures and omit streaming scratch fields; a correct Go struct alone cannot prove that boundary.
func assertAnthropicSSEContentJSON(t *testing.T, result *AssistantMessage, want string) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if errorMessage, present := fields["errorMessage"]; present {
		t.Fatalf("successful result has errorMessage=%s, want omitted", errorMessage)
	}
	assertShapeJSON(t, fields["content"], want)
}
