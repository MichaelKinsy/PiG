package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func managedAnthropicContext(level string, otherProvider bool) Context {
	provider := "anthropic"
	if otherProvider {
		provider = "other-provider"
	}
	return Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}, AssistantMessage{API: APIAnthropicMessages, Provider: provider, Model: "claude-fable-5-1", ProviderThinkingLevel: level, StopReason: StopReasonStop, Content: []AssistantContentBlock{ThinkingContent{Thinking: "reasoning", ThinkingSignature: "signature"}, TextContent{Text: "answer"}}, Timestamp: 1}, UserMessage{Content: UserText("two"), Timestamp: 2}}}
}

// packages/ai/test/anthropic-mid-conversation-effort.test.ts:66-82,117: the captured request fails, but its result still carries the active effort.
func TestAnthropicMidConversationEffortPayloadFailureRetainsMetadata(t *testing.T) {
	provider := NewAnthropicProvider(AnthropicConfig{Model: "claude-fable-5-1", ProviderID: "anthropic", APIKey: "test-key", BaseURL: "http://127.0.0.1:9"})
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}}), StreamOptions{ThinkingEnabled: new(true), Effort: "low", CacheRetention: CacheRetentionNone, OnPayload: func(any, *Model) (any, error) {
		return nil, errors.New("payload captured")
	}})
	if err != nil {
		t.Fatalf("setup failure discarded assistant metadata: %v", err)
	}
	message := stream.Result()
	if message.ProviderThinkingLevel != "low" {
		t.Fatalf("providerThinkingLevel=%q, want low even on payload rejection", message.ProviderThinkingLevel)
	}
}

func TestAnthropicUpstreamMidConversationEffort(t *testing.T) {
	config := AnthropicConfig{Model: "claude-fable-5-1", ProviderID: "anthropic", Compat: &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true), SupportsMidConvoEffort: new(true)}, ModelMetadata: &Model{ID: "claude-fable-5-1", DisplayName: "Claude Fable 5.1", Input: []string{"text"}, Capabilities: ModelCapabilities{MaxThinking: ThinkingMax, ContextWindow: 200000, MaxOutputTokens: 32000}, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, BaseURL: "http://127.0.0.1:9", Reasoning: true}, ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: new("low"), ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingMax: new("max")}}}
	capture := func(t *testing.T, cfg AnthropicConfig, ctx Context, effort ThinkingLevel) (*AssistantMessage, map[string]json.RawMessage) {
		t.Helper()
		cfg.APIKey = "test-key"
		cfg.BaseURL = "http://127.0.0.1:9"
		provider := NewAnthropicProvider(cfg)
		defer func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		}()
		var payload map[string]json.RawMessage
		stream, err := provider.Stream(t.Context(), NormalizeContext(ctx), StreamOptions{ThinkingEnabled: new(true), Effort: string(effort), CacheRetention: CacheRetentionNone, OnPayload: func(value any, _ *Model) (any, error) {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				return nil, err
			}
			return nil, errors.New("payload captured")
		}})
		if err != nil {
			t.Fatalf("setup failure discarded assistant metadata: %v", err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonError || result.ErrorMessage != "payload captured" {
			t.Fatalf("capture must reject before HTTP execution: %+v", result)
		}
		terminalEvents := 0
		for event := range stream.Events(t.Context()) {
			terminal, ok := event.(ErrorEvent)
			if !ok || event.EventType() != EventError || terminal.Reason != StopReasonError || !reflect.DeepEqual(terminal.Error, result) {
				t.Fatalf("payload rejection emitted %T instead of the matching error result", event)
			}
			terminalEvents++
		}
		// Pi's catch emits exactly one terminal error and no start for this rejected onPayload call.
		if terminalEvents != 1 {
			t.Fatalf("terminal events=%d, want one rejected-request error", terminalEvents)
		}
		if payload == nil {
			t.Fatal("onPayload was not called")
		}
		return result, payload
	}
	markers := func(t *testing.T, payload map[string]json.RawMessage) []map[string]json.RawMessage {
		t.Helper()
		var messages []map[string]json.RawMessage
		if err := json.Unmarshal(payload["messages"], &messages); err != nil {
			t.Fatal(err)
		}
		result := []map[string]json.RawMessage{}
		for _, message := range messages {
			if string(message["role"]) == `"system"` {
				result = append(result, message)
			}
		}
		return result
	}
	// .upstream/v0.87.1/packages/ai/test/anthropic-mid-conversation-effort.test.ts:92
	t.Run("reconstructs an exact historical marker prefix and appends the current marker", func(t *testing.T) {
		first, payload := capture(t, config, Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}}, ThinkingLow)
		_, second := capture(t, config, managedAnthropicContext("low", false), ThinkingHigh)
		assertShapeJSON(t, payload["messages"], `[{"role":"user","content":"one"},{"role":"system","content":[],"output_config":{"effort":"low"}}]`)
		var prefix, all []json.RawMessage
		if err := json.Unmarshal(payload["messages"], &prefix); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(second["messages"], &all); err != nil {
			t.Fatal(err)
		}
		if len(all) < len(prefix) {
			t.Fatalf("messages=%s", second["messages"])
		}
		encoded, err := json.Marshal(all[:len(prefix)])
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, string(payload["messages"]))
		assertShapeJSON(t, all[len(all)-1], `{"role":"system","content":[],"output_config":{"effort":"high"}}`)
		assertShapeJSON(t, payload["output_config"], `{"effort":"high"}`)
		assertShapeJSON(t, second["output_config"], `{"effort":"high"}`)
		assertShapeJSON(t, second["thinking"], `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`)
		if first.ProviderThinkingLevel != "low" {
			t.Fatalf("level=%q", first.ProviderThinkingLevel)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-mid-conversation-effort.test.ts:121 (all five rows)
	for _, effort := range []ThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax} {
		t.Run("preserves native effort "+string(effort), func(t *testing.T) {
			result, payload := capture(t, config, Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}}, effort)
			encoded, err := json.Marshal(markers(t, payload))
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, encoded, `[{"role":"system","content":[],"output_config":{"effort":"`+string(effort)+`"}}]`)
			if result.ProviderThinkingLevel != string(effort) {
				t.Fatalf("level=%q", result.ProviderThinkingLevel)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/anthropic-mid-conversation-effort.test.ts:128
	t.Run("defaults omitted effort to high and still enables drop_block", func(t *testing.T) {
		result, payload := capture(t, config, Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}}, "")
		var messages []json.RawMessage
		if err := json.Unmarshal(payload["messages"], &messages); err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, messages[len(messages)-1], `{"role":"system","content":[],"output_config":{"effort":"high"}}`)
		var thinking struct {
			BlockBinding struct {
				PrefixMismatchBehavior string `json:"prefix_mismatch_behavior"`
			} `json:"block_binding"`
		}
		if err := json.Unmarshal(payload["thinking"], &thinking); err != nil {
			t.Fatal(err)
		}
		if thinking.BlockBinding.PrefixMismatchBehavior != "drop_block" || result.ProviderThinkingLevel != "high" {
			t.Fatalf("thinking=%s level=%q", payload["thinking"], result.ProviderThinkingLevel)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-mid-conversation-effort.test.ts:139
	t.Run("does not invent markers for legacy or other-provider assistants", func(t *testing.T) {
		ctx := managedAnthropicContext("", false)
		other := managedAnthropicContext("low", true)
		ctx.Messages = append(ctx.Messages, other.Messages[1], UserMessage{Content: UserText("three"), Timestamp: 3})
		_, payload := capture(t, config, ctx, ThinkingMedium)
		encoded, err := json.Marshal(markers(t, payload))
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, `[{"role":"system","content":[],"output_config":{"effort":"medium"}}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-mid-conversation-effort.test.ts:151
	t.Run("leaves unsupported models on top-level effort", func(t *testing.T) {
		cfg := config
		cfg.Compat = &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true)}
		result, payload := capture(t, cfg, Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}}, ThinkingLow)
		assertShapeJSON(t, payload["messages"], `[{"role":"user","content":"one"}]`)
		assertShapeJSON(t, payload["output_config"], `{"effort":"low"}`)
		assertShapeJSON(t, payload["thinking"], `{"type":"adaptive","display":"summarized"}`)
		if result.ProviderThinkingLevel != "" {
			t.Fatalf("level=%q", result.ProviderThinkingLevel)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-mid-conversation-effort.test.ts:161
	t.Run("sends the effort and binding beta headers", func(t *testing.T) {
		// Preserve the original three events, usage/model values, input and injected-fetch boundary.
		body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"model\":\"claude-fable-5-1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		var betaHeader string
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			defer request.Body.Close()
			betaHeader = request.Header.Get("Anthropic-Beta")
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}
		cfg := config
		cfg.APIKey, cfg.BaseURL = "test-key", "http://127.0.0.1:9"
		provider := NewAnthropicProvider(cfg)
		t.Cleanup(func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		})
		input := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}})
		stream, err := provider.Stream(t.Context(), input, StreamOptions{CacheRetention: CacheRetentionNone, Fetch: client})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonStop {
			t.Fatalf("result=%+v", result)
		}
		for _, beta := range []string{"mid-conversation-output-config-2026-07-01", "thinking-binding-controls-2026-08-01"} {
			if !headerListContains(betaHeader, beta) {
				t.Errorf("missing beta %s", beta)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-mid-conversation-effort.test.ts:196
	t.Run("generates exact model and transport gates", func(t *testing.T) {
		direct := upstreamCatalogModel(t, "anthropic", "claude-fable-5-1")
		router := upstreamCatalogModel(t, "openrouter", "anthropic/claude-fable-5.1")
		unsupported := upstreamCatalogModel(t, "anthropic", "claude-opus-4-8")
		if direct.ProviderMeta.Compat == nil || direct.ProviderMeta.Compat.SupportsMidConvoEffort == nil || !*direct.ProviderMeta.Compat.SupportsMidConvoEffort {
			t.Fatal("direct missing mid-conversation effort")
		}
		off, ok := direct.ThinkingLevelMap[ThinkingOff]
		if !ok || off != nil {
			t.Fatal("direct off is not null")
		}
		if router.ProviderMeta.API != APIAnthropicMessages || router.ProviderMeta.BaseURL != "https://openrouter.ai/api" || router.ProviderMeta.Compat == nil || router.ProviderMeta.Compat.SupportsMidConvoEffort == nil || !*router.ProviderMeta.Compat.SupportsMidConvoEffort {
			t.Fatalf("router=%+v", router)
		}
		if unsupported.ProviderMeta.Compat != nil && unsupported.ProviderMeta.Compat.SupportsMidConvoEffort != nil {
			t.Fatal("unsupported model advertises managed effort")
		}
		routerOpus := upstreamCatalogModel(t, "openrouter", "anthropic/claude-opus-5")
		if routerOpus.ProviderMeta.Compat != nil && routerOpus.ProviderMeta.Compat.SupportsMidConvoEffort != nil {
			t.Fatal("OpenRouter Opus advertises managed effort")
		}
		opus := upstreamCatalogModel(t, "anthropic", "claude-opus-5")
		if opus.ProviderMeta.Compat != nil && !reflect.DeepEqual(opus.ProviderMeta.Compat.AllowedFallbackModels, []AnthropicAllowedFallbackModel(nil)) {
			t.Fatal("unexpected Opus fallbacks")
		}
	})
}
