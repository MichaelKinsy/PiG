package codingagent_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

const thinkingDropNotice = "Anthropic dropped 3 thinking blocks: prefix_binding_mismatch (details in session)"

func openAIReasoningDropReply(timestamp int64, ids, reasons []string) scriptedReply {
	return func() *ai.AssistantMessage {
		transformations := make([]any, 0, len(ids))
		for i, id := range ids {
			reason := reasons[min(i, len(reasons)-1)]
			transformations = append(transformations, map[string]any{
				"type":   "reasoning",
				"id":     id,
				"action": "removed",
				"reason": reason,
			})
		}
		return &ai.AssistantMessage{
			Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "survived"}},
			API:        ai.APIOpenAIResponses,
			Provider:   "openai",
			Model:      "gpt-6.2-sol",
			Usage:      ai.Usage{Input: 1, Output: 1, TotalTokens: 2},
			StopReason: ai.StopReasonStop,
			Timestamp:  timestamp,
			Diagnostics: []ai.AssistantMessageDiagnostic{{
				Type:      "openai_input_transformations",
				Timestamp: timestamp,
				Details:   map[string]any{"transformations": transformations},
			}},
		}
	}
}

func thinkingDropReply(timestamp int64) scriptedReply {
	return func() *ai.AssistantMessage {
		transformations := make([]any, 0, 3)
		for _, index := range []int{2, 5, 8} {
			transformations = append(transformations, map[string]any{"type": "thinking_dropped", "path": fmt.Sprintf("messages.%d.content.0", index), "reason": "prefix_binding_mismatch"})
		}
		return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "survived"}}, API: "anthropic-messages", Provider: "anthropic", Model: "claude-fable-5-1", Usage: ai.Usage{Input: 1, Output: 1, TotalTokens: 2}, StopReason: ai.StopReasonStop, Timestamp: timestamp, Diagnostics: []ai.AssistantMessageDiagnostic{{Type: "anthropic_input_transformations", Timestamp: 1, Details: map[string]any{"transformations": transformations}}}}
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-assistant-diagnostics.test.ts:63
func TestAssistantDiagnosticsShowsThinkingDropsWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			pair := newSessionPair(t, fmt.Sprintf(`{"showCacheMissNotices":%t,"retry":{"enabled":false}}`, enabled), 200000, nil, thinkingDropReply(1))
			pair.harness.Do(func() { pair.harness.Enter("question") })
			pair.harness.WaitIdle(t, 10*time.Second)
			output := pair.harness.Chat()
			if got := strings.Contains(output, thinkingDropNotice); got != enabled {
				t.Fatalf("notice present = %v, want %v: %q", got, enabled, output)
			}
		})
	}
}

func TestAssistantDiagnosticsNewSessionResetsThinkingDropComparison(t *testing.T) {
	pair := newSessionPair(t, `{"showCacheMissNotices":true,"retry":{"enabled":false}}`, 200000, nil, thinkingDropReply(1), thinkingDropReply(2))
	pair.harness.Do(func() { pair.harness.Enter("first") })
	pair.harness.WaitIdle(t, 10*time.Second)
	// /new resolves a fresh Model Runtime from the catalog. Register the scripted provider so the replacement uses the same replies without rebinding an outgoing model.
	if err := pair.session.Services().Registry().RegisterProvider("faux", coding.ProviderConfigInput{
		API: ai.APIOpenAICompletions, APIKey: "fixture-key", BaseURL: "https://faux.invalid/v1", Models: []ai.AnyModel{pair.session.Model()},
		StreamSimple: func(ctx context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return pair.provider.Stream(ctx, transcript, options)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := pair.harness.ReplaceSession("new", ""); err != nil {
		t.Fatal(err)
	}
	pair.harness.Do(func() { pair.harness.Enter("second") })
	pair.harness.WaitIdle(t, 10*time.Second)
	if got := strings.Count(pair.harness.Chat(), thinkingDropNotice); got != 1 {
		t.Fatalf("new session notice count = %d, want 1: %q", got, pair.harness.Chat())
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-assistant-diagnostics.test.ts:83
func TestAssistantDiagnosticsDoesNotRepeatUnchangedThinkingDrops(t *testing.T) {
	pair := newSessionPair(t, `{"showCacheMissNotices":true,"retry":{"enabled":false}}`, 200000, nil, thinkingDropReply(1), thinkingDropReply(2))
	for _, question := range []string{"first", "second"} {
		pair.harness.Do(func() { pair.harness.Enter(question) })
		pair.harness.WaitIdle(t, 10*time.Second)
		if got := strings.Count(pair.harness.Chat(), thinkingDropNotice); got != 1 {
			t.Fatalf("%s turn: notice count = %d, want 1: %q", question, got, pair.harness.Chat())
		}
	}
}

// Ports packages/coding-agent/test/interactive-mode-assistant-diagnostics.test.ts (Pi 03de604b8).
func TestAssistantDiagnosticsShowsOpenAIReasoningRemovalsWithReasons(t *testing.T) {
	pair := newSessionPair(
		t,
		`{"showCacheMissNotices":true,"retry":{"enabled":false}}`,
		200000,
		nil,
		thinkingDropReply(1),
		openAIReasoningDropReply(2, []string{"rs_1", "rs_2"}, []string{"prefix_mismatch", "organization_mismatch"}),
	)
	pair.harness.Do(func() { pair.harness.Enter("first") })
	pair.harness.WaitIdle(t, 10*time.Second)
	// Previous Anthropic response dropped 3 blocks; that must not hide the 2 OpenAI removals.
	pair.harness.Do(func() { pair.harness.Enter("second") })
	pair.harness.WaitIdle(t, 10*time.Second)
	output := pair.harness.Chat()
	want := "OpenAI dropped 2 reasoning items: prefix_mismatch, organization_mismatch (details in session)"
	if !strings.Contains(output, want) {
		t.Fatalf("missing OpenAI notice %q in output: %q", want, output)
	}
}

// Ports packages/coding-agent/test/interactive-mode-assistant-diagnostics.test.ts (Pi 03de604b8).
func TestAssistantDiagnosticsShowsOpenAIReasoningRemovalsOnlyWhenNewItemsRemoved(t *testing.T) {
	pair := newSessionPair(
		t,
		`{"showCacheMissNotices":true,"retry":{"enabled":false}}`,
		200000,
		nil,
		openAIReasoningDropReply(1, []string{"rs_1", "rs_2"}, []string{"prefix_mismatch"}),
		openAIReasoningDropReply(2, []string{"rs_1", "rs_2"}, []string{"prefix_mismatch"}),
		openAIReasoningDropReply(3, []string{"rs_1", "rs_2", "rs_3"}, []string{"prefix_mismatch"}),
	)
	pair.harness.Do(func() { pair.harness.Enter("first") })
	pair.harness.WaitIdle(t, 10*time.Second)
	wantTwo := "OpenAI dropped 2 reasoning items: prefix_mismatch (details in session)"
	if got := strings.Count(pair.harness.Chat(), wantTwo); got != 1 {
		t.Fatalf("first turn: notice count = %d, want 1: %q", got, pair.harness.Chat())
	}
	pair.harness.Do(func() { pair.harness.Enter("second") })
	pair.harness.WaitIdle(t, 10*time.Second)
	if got := strings.Count(pair.harness.Chat(), wantTwo); got != 1 {
		t.Fatalf("second turn: notice count = %d, want 1: %q", got, pair.harness.Chat())
	}
	pair.harness.Do(func() { pair.harness.Enter("third") })
	pair.harness.WaitIdle(t, 10*time.Second)
	wantThree := "OpenAI dropped 3 reasoning items: prefix_mismatch (details in session)"
	if got := strings.Count(pair.harness.Chat(), wantThree); got != 1 {
		t.Fatalf("third turn: notice count = %d, want 1: %q", got, pair.harness.Chat())
	}
}
