package ai

import (
	"encoding/json"
	"testing"
)

// streamAnthropicInjected streams one "Hello" turn through an injected client, as anthropic-sse-parsing.test.ts passes `{ client }` with no API key, and returns the result and the params the client received.
func streamAnthropicInjected(t *testing.T, config AnthropicConfig, opts StreamOptions) (*AssistantMessage, AnthropicCreateParams) {
	t.Helper()
	client := &injectedAnthropicClient{sse: anthropicMinimalFixture()}
	opts.AnthropicClient = client
	result := runInjectedAnthropic(t, config, opts)
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.calls) != 1 {
		t.Fatalf("injected client calls = %d, want 1 (%s)", len(client.calls), result.ErrorMessage)
	}
	return result, client.calls[0]
}

// Ports the cases of packages/ai/test/anthropic-sse-parsing.test.ts that inject a fake client (`client.beta.messages.create(params)`) and assert the params it receives: params.stream and params.betas. TestAnthropicUpstreamSSEParsing keeps the HTTP-transport forms of the same cases.
func TestAnthropicUpstreamSSEParsingInjectedClient(t *testing.T) {
	// anthropic-sse-parsing.test.ts:246-269
	t.Run("forces streaming after an onPayload replacement", func(t *testing.T) {
		_, params := streamAnthropicInjected(t, AnthropicConfig{Model: "claude-fable-5-1", ProviderID: "anthropic"}, StreamOptions{OnPayload: func(value any, _ *Model) (any, error) {
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
		var body map[string]json.RawMessage
		if err := json.Unmarshal(params.Body, &body); err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, body["stream"], "true")
	})
	// anthropic-sse-parsing.test.ts:271-291
	t.Run("omits the interleaved-thinking beta when thinking is disabled", func(t *testing.T) {
		_, params := streamAnthropicInjected(t, AnthropicConfig{Model: "anthropic/claude-haiku-4.5", ProviderID: "openrouter"}, StreamOptions{ThinkingEnabled: new(false)})
		if params.Betas != nil && headerListContains(*params.Betas, "interleaved-thinking-2025-05-14") {
			t.Fatalf("betas = %q, want no interleaved-thinking beta", *params.Betas)
		}
	})
	// anthropic-sse-parsing.test.ts:293-315
	t.Run("passes managed beta features to injected clients", func(t *testing.T) {
		result, params := streamAnthropicInjected(t, AnthropicConfig{Model: "claude-fable-5-1", ProviderID: "anthropic"}, StreamOptions{})
		if result.StopReason != StopReasonStop {
			t.Fatalf("stop reason = %s (%s), want stop", result.StopReason, result.ErrorMessage)
		}
		if params.Betas == nil {
			t.Fatal("the injected client received no betas")
		}
		for _, beta := range []string{"mid-conversation-output-config-2026-07-01", "thinking-binding-controls-2026-08-01"} {
			if !headerListContains(*params.Betas, beta) {
				t.Errorf("betas = %q, missing %s", *params.Betas, beta)
			}
		}
	})
}
