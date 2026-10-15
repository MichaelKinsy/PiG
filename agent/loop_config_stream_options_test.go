package agent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/src/types.ts AgentLoopConfig extends SimpleStreamOptions, so each SimpleStreamOptions property is a property of the config and reaches the provider request that the loop builds.
func TestAgentLoopConfigInheritsEverySimpleStreamOption(t *testing.T) {
	retries, retryDelay, timeout, connectTimeout := 3, 1500, 9000, 750
	budgets := &ai.ThinkingBudgets{}
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	config.APIKey = "static-key"
	config.CacheRetention = ai.CacheRetention("long")
	config.Env = ai.ProviderEnv{"PIG_TEST_ENV": "env-value"}
	header := "header-value"
	config.Headers = ai.ProviderHeaders{"x-test": &header}
	config.MaxRetries = &retries
	config.MaxRetryDelayMs = &retryDelay
	config.MaxTokens = 321
	config.Metadata = map[string]any{"user_id": "u1"}
	config.SamplingParams = map[string]any{"top_k": 7}
	config.SessionID = "session-1"
	config.Temperature = 0.25
	config.TemperatureSet = true
	config.ThinkingBudgets = budgets
	config.TimeoutMs = &timeout
	config.Transport = ai.Transport("websocket")
	config.WebSocketConnectTimeoutMs = &connectTimeout
	payloads := 0
	config.OnPayload = func(payload any, _ *ai.Model) (any, error) { payloads++; return payload, nil }

	if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err != nil {
		t.Fatal(err)
	}
	got := provider.request(1).opts
	if got.APIKey != "static-key" || got.CacheRetention != "long" || got.Env["PIG_TEST_ENV"] != "env-value" || got.Headers["x-test"] == nil || *got.Headers["x-test"] != "header-value" {
		t.Fatalf("credential, cache, env or header option lost: %+v", got)
	}
	if got.MaxRetries != &retries || got.MaxRetryDelayMs != &retryDelay || got.TimeoutMs != &timeout || got.WebSocketConnectTimeoutMs != &connectTimeout {
		t.Fatalf("retry or timeout option lost: %+v", got)
	}
	if got.MaxTokens != 321 || got.Temperature != 0.25 || !got.TemperatureSet || got.SamplingParams["top_k"] != 7 || got.Metadata["user_id"] != "u1" {
		t.Fatalf("sampling or output option lost: %+v", got)
	}
	if got.SessionID != "session-1" || got.Transport != "websocket" || got.ThinkingBudgets != budgets {
		t.Fatalf("session, transport or budgets lost: %+v", got)
	}
	if got.OnPayload == nil {
		t.Fatal("OnPayload did not reach the provider request")
	}
	if _, err := got.OnPayload(map[string]any{}, nil); err != nil || payloads != 1 {
		t.Fatalf("OnPayload calls = %d, err = %v", payloads, err)
	}
}

// upstream: packages/agent/src/agent-loop.ts runLoop passes `thinkingLevel: config.reasoning ?? "off"` to prepareRequest and spreads config (with reasoning) into every request, so the inherited SimpleStreamOptions.reasoning is the loop's thinking level. An unset reasoning is "off" for prepareRequest and absent on the request.
func TestAgentLoopConfigReasoningIsTheLoopThinkingLevel(t *testing.T) {
	for name, tc := range map[string]struct {
		reasoning   ai.ThinkingLevel
		wantPrepare ai.ModelThinkingLevel
	}{
		"set":   {reasoning: ai.ThinkingLevel("high"), wantPrepare: ai.ThinkingHigh},
		"unset": {reasoning: "", wantPrepare: ai.ThinkingOff},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &scriptedProvider{respond: replyText("ok")}
			config := standaloneConfig(provider)
			config.Thinking = tc.reasoning
			var prepared []ai.ModelThinkingLevel
			config.PrepareRequest = func(_ context.Context, request PrepareRequestContext) (*AgentRequestUpdate, error) {
				prepared = append(prepared, request.ThinkingLevel)
				return nil, nil
			}
			if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err != nil {
				t.Fatal(err)
			}
			if len(prepared) != 1 || prepared[0] != tc.wantPrepare {
				t.Fatalf("prepareRequest thinking levels = %v, want [%s]", prepared, tc.wantPrepare)
			}
			if got := provider.request(1).opts.Thinking; got != tc.reasoning {
				t.Fatalf("request reasoning = %q, want %q", got, tc.reasoning)
			}
		})
	}
}
