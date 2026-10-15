//go:build live

package ai

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Pi 1.2.0 packages/ai/test/radius-thinking-replay-e2e.test.ts: Radius drops replayed Anthropic thinking (e2e, needs real
// credentials). Each case makes three paid requests; the Claude reply's signed thinking must reach the follow-up request,
// which shows as input tokens roughly the size of the reply's thinking.

var radiusReplayTool = ToolSchema{
	Name:        "lookup",
	Description: "Look up a value by key.",
	Parameters: JsonObject{
		"type":       "object",
		"properties": JsonObject{"key": JsonObject{"type": "string"}},
		"required":   []any{"key"},
	},
}

var radiusReplayUser = UserMessage{
	Content: UserText("First, think carefully: list every prime between 100 and 300 in your head and sum them. " +
		"Then call the lookup tool with key equal to that sum. Do not write the primes in your visible reply."),
	Timestamp: 1,
}

// radiusReplayInputTokens is the input tokens the provider reports for the request, cached or not.
func radiusReplayInputTokens(message *AssistantMessage) int {
	return message.Usage.Input + message.Usage.CacheRead + message.Usage.CacheWrite
}

func radiusReplayRequest(t *testing.T, ctx context.Context, model *Model, apiKey string, messages []Message) *AssistantMessage {
	t.Helper()
	request := Context{SystemPrompt: "You are a careful assistant.", Messages: messages, Tools: []ToolSchema{radiusReplayTool}}
	message, err := CompleteSimple(ctx, model, request, StreamOptions{APIKey: apiKey, Thinking: ThinkingLevelHigh, MaxTokens: 16000, CacheRetention: CacheRetentionNone})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

// radiusReplayThinkingTurn is a real reply with signed thinking and a tool call, plus the tool result that answers it.
func radiusReplayThinkingTurn(t *testing.T, ctx context.Context, model *Model, apiKey string) (*AssistantMessage, ToolResultMessage) {
	t.Helper()
	reply := radiusReplayRequest(t, ctx, model, apiKey, []Message{radiusReplayUser})
	if reply.StopReason != StopReasonToolUse {
		t.Fatalf("stopReason=%s error=%s", reply.StopReason, reply.ErrorMessage)
	}
	signed := false
	var call *ToolCall
	for _, block := range reply.Content {
		switch block := block.(type) {
		case ThinkingContent:
			if block.ThinkingSignature != "" {
				signed = true
			}
		case ToolCall:
			if call == nil {
				call = &block
			}
		}
	}
	if !signed {
		t.Fatal("reply has no signed thinking block")
	}
	if call == nil {
		t.Fatal("reply has no tool call")
	}
	result := ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: "value: 42"}}, IsError: false, Timestamp: 2}
	return reply, result
}

// radiusReplayedThinkingTokens is the input tokens reply's thinking adds to the follow-up request.
func radiusReplayedThinkingTokens(t *testing.T, ctx context.Context, model *Model, apiKey string, reply AssistantMessage, result ToolResultMessage) int {
	t.Helper()
	withoutThinking := reply
	withoutThinking.Content = nil
	for _, block := range reply.Content {
		if _, thinking := block.(ThinkingContent); !thinking {
			withoutThinking.Content = append(withoutThinking.Content, block)
		}
	}
	var kept, stripped *AssistantMessage
	var keptErr, strippedErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		kept, keptErr = CompleteSimple(ctx, model, Context{SystemPrompt: "You are a careful assistant.", Messages: []Message{radiusReplayUser, reply, result}, Tools: []ToolSchema{radiusReplayTool}}, StreamOptions{APIKey: apiKey, Thinking: ThinkingLevelHigh, MaxTokens: 16000, CacheRetention: CacheRetentionNone})
	}()
	go func() {
		defer wg.Done()
		stripped, strippedErr = CompleteSimple(ctx, model, Context{SystemPrompt: "You are a careful assistant.", Messages: []Message{radiusReplayUser, withoutThinking, result}, Tools: []ToolSchema{radiusReplayTool}}, StreamOptions{APIKey: apiKey, Thinking: ThinkingLevelHigh, MaxTokens: 16000, CacheRetention: CacheRetentionNone})
	}()
	wg.Wait()
	if keptErr != nil || strippedErr != nil {
		t.Fatalf("kept err=%v stripped err=%v", keptErr, strippedErr)
	}
	if kept.StopReason == StopReasonError {
		t.Fatalf("kept stopReason=error: %s", kept.ErrorMessage)
	}
	if stripped.StopReason == StopReasonError {
		t.Fatalf("stripped stopReason=error: %s", stripped.ErrorMessage)
	}
	return radiusReplayInputTokens(kept) - radiusReplayInputTokens(stripped)
}

func radiusReplayModel(t *testing.T, provider string) *Model {
	t.Helper()
	model := GetBuiltinModel(provider, "claude-opus-5-5")
	if model == nil {
		t.Fatalf("no built-in model %s/claude-opus-5-5", provider)
	}
	return model
}

func TestRadiusThinkingReplayE2E(t *testing.T) {
	// Pi 1.2.0 packages/ai/test/radius-thinking-replay-e2e.test.ts:112 "anthropic keeps it (control)"
	t.Run("replayed Claude thinking stays in context/anthropic keeps it (control)", func(t *testing.T) {
		key := liveProviderKey(t, "anthropic")
		ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
		defer cancel()
		model := radiusReplayModel(t, "anthropic")
		reply, result := radiusReplayThinkingTurn(t, ctx, model, key)
		if reply.ProviderThinkingLevel != "high" {
			t.Fatalf("providerThinkingLevel=%q, want high", reply.ProviderThinkingLevel)
		}
		added := radiusReplayedThinkingTokens(t, ctx, model, key, *reply, result)
		if float64(added) <= 0.5*float64(reply.Usage.Output) {
			t.Fatalf("added=%d, want > 0.5 * %d", added, reply.Usage.Output)
		}
	})

	// Pi 1.2.0 packages/ai/test/radius-thinking-replay-e2e.test.ts:120 "radius keeps it"
	t.Run("replayed Claude thinking stays in context/radius keeps it", func(t *testing.T) {
		key := liveProviderKey(t, "radius")
		ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
		defer cancel()
		model := radiusReplayModel(t, "radius")
		reply, result := radiusReplayThinkingTurn(t, ctx, model, key)
		// Cause: without this level, the replayed block has no effort marker and Anthropic drops it (expect.soft).
		if reply.ProviderThinkingLevel != "high" {
			t.Errorf("radius done event omits providerThinkingLevel: got %q, want high", reply.ProviderThinkingLevel)
		}
		// Effect: the follow-up request reports no input tokens for the thinking.
		added := radiusReplayedThinkingTokens(t, ctx, model, key, *reply, result)
		if float64(added) <= 0.5*float64(reply.Usage.Output) {
			t.Fatalf("radius dropped the replayed thinking: added=%d, want > 0.5 * %d", added, reply.Usage.Output)
		}
	})

	// Pi 1.2.0 packages/ai/test/radius-thinking-replay-e2e.test.ts:130 "radius keeps it when the client supplies the level"
	t.Run("replayed Claude thinking stays in context/radius keeps it when the client supplies the level", func(t *testing.T) {
		key := liveProviderKey(t, "radius")
		ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
		defer cancel()
		model := radiusReplayModel(t, "radius")
		reply, result := radiusReplayThinkingTurn(t, ctx, model, key)
		leveled := *reply
		leveled.ProviderThinkingLevel = "high"
		added := radiusReplayedThinkingTokens(t, ctx, model, key, leveled, result)
		if float64(added) <= 0.5*float64(reply.Usage.Output) {
			t.Fatalf("added=%d, want > 0.5 * %d", added, reply.Usage.Output)
		}
	})
}
