// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package compaction

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

type summaryModels struct {
	responses []ai.AssistantMessage
	options   []ai.StreamOptions
	requests  []ai.Context
}

func (m *summaryModels) CompleteSimple(_ context.Context, _ *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
	m.options = append(m.options, options)
	m.requests = append(m.requests, request)
	if len(m.responses) == 0 {
		return &ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: "No faux completeSimple response queued"}
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return &response
}
func summaryResponse(text string, reason ai.StopReason, message string) ai.AssistantMessage {
	return ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: reason, ErrorMessage: message, Usage: *mockUsage(100, 50, 0, 0)}
}
func summaryModel(reasoning bool, maxTokens int) *ai.Model {
	model := &ai.Model{ID: "non-reasoning-model", Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: maxTokens}}
	if reasoning {
		model.ID = "reasoning-model"
		model.Capabilities.MaxThinking = ai.ThinkingHigh
	}
	return model
}
func requestUserText(t *testing.T, models *summaryModels) string {
	t.Helper()
	if len(models.requests) == 0 {
		t.Fatal("no summary request")
	}
	for _, message := range models.requests[0].Messages {
		if user, ok := message.(ai.UserMessage); ok {
			switch content := user.Content.(type) {
			case ai.UserText:
				return string(content)
			case ai.UserContentBlocks:
				var text strings.Builder
				for _, block := range content {
					if block, ok := block.(ai.TextContent); ok {
						text.WriteString(block.Text)
					}
				}
				return text.String()
			}
		}
	}
	t.Fatal("no user message")
	return ""
}
func assertCompactionFailure(t *testing.T, err error, code harness.CompactionErrorCode, message string) {
	t.Helper()
	var failure *harness.CompactionError
	if !errors.As(err, &failure) || failure.Code != code || failure.Message != message {
		t.Fatalf("error=%v; want %s/%s", err, code, message)
	}
}
func generationPreparation(messages []agent.AgentMessage, split bool) CompactionPreparation {
	p := CompactionPreparation{MessagesToSummarize: messages, TurnPrefixMessages: []agent.AgentMessage{}, RetainedTail: messages, IsSplitTurn: split, TokensBefore: 100, FileOps: CreateFileOps(), Settings: CompactionSettings{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20}}
	if split {
		p.MessagesToSummarize = []agent.AgentMessage{}
		p.TurnPrefixMessages = messages
	}
	return p
}

func TestHarnessCompactionGenerationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:440
	t.Run("passes reasoning through generateSummary only for reasoning models with thinking enabled", func(t *testing.T) {
		for _, test := range []struct {
			reasoning   bool
			level, want ai.ThinkingLevel
		}{{true, ai.ThinkingMedium, ai.ThinkingMedium}, {true, ai.ThinkingOff, ""}, {false, ai.ThinkingMedium, ""}} {
			models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("## Goal\nTest summary", ai.StopReasonStop, "")}}
			if _, err := GenerateSummary(t.Context(), []agent.AgentMessage{user("Summarize this.")}, models, summaryModel(test.reasoning, 8192), 2000, "", "", test.level, nil, ai.RetryCallbacks{}); err != nil {
				t.Fatal(err)
			}
			if len(models.options) != 1 || models.options[0].Thinking != test.want {
				t.Fatalf("options=%+v", models.options)
			}
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:513
	t.Run("includes previous summaries and custom instructions in prompts", func(t *testing.T) {
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("## Goal\nTest summary", ai.StopReasonStop, "")}}
		summary, err := GenerateSummaryWithUsage(t.Context(), []agent.AgentMessage{user("Summarize this.")}, models, summaryModel(false, 8192), 2000, "focus", "old summary", "", nil, ai.RetryCallbacks{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(summary.Text, "Test summary") || summary.Usage.Input <= 0 || summary.Usage.Output <= 0 || summary.Usage.TotalTokens != summary.Usage.Input+summary.Usage.Output+summary.Usage.CacheRead+summary.Usage.CacheWrite {
			t.Fatal(summary)
		}
		prompt := requestUserText(t, models)
		if !strings.Contains(prompt, "<previous-summary>\nold summary\n</previous-summary>") || !strings.Contains(prompt, "Additional focus: focus") {
			t.Fatal(prompt)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:552
	t.Run("preserves string result from generateSummary", func(t *testing.T) {
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("## Goal\nTest summary", ai.StopReasonStop, "")}}
		summary, err := GenerateSummary(t.Context(), []agent.AgentMessage{user("Summarize this.")}, models, summaryModel(false, 8192), 2000, "", "", "", nil, ai.RetryCallbacks{})
		if err != nil || summary != "## Goal\nTest summary" {
			t.Fatalf("summary=%q error=%v", summary, err)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:575
	t.Run("returns error results for failed or aborted generations", func(t *testing.T) {
		for _, test := range []struct {
			reason        ai.StopReason
			message, want string
			code          harness.CompactionErrorCode
		}{{ai.StopReasonError, "boom", "Summarization failed: boom", harness.CompactionErrorSummarizationFailed}, {ai.StopReasonAborted, "stopped", "stopped", harness.CompactionErrorAborted}} {
			models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("", test.reason, test.message)}}
			_, err := GenerateSummary(t.Context(), []agent.AgentMessage{user("Summarize this.")}, models, summaryModel(false, 8192), 2000, "", "", "", nil, ai.RetryCallbacks{})
			assertCompactionFailure(t, err, test.code, test.want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:613
	t.Run("clamps summary maxTokens to model output cap", func(t *testing.T) {
		messages := []agent.AgentMessage{user("Summarize this.")}
		p := generationPreparation(messages, true)
		p.MessagesToSummarize = messages
		p.TokensBefore = 600000
		p.Settings = CompactionSettings{Enabled: true, ReserveTokens: 500000, KeepRecentTokens: 20000}
		response := summaryResponse("## Goal\nTest summary", ai.StopReasonStop, "")
		models := &summaryModels{responses: []ai.AssistantMessage{response, response}}
		if _, err := Compact(t.Context(), p, models, summaryModel(false, 128000), "", "", nil, ai.RetryCallbacks{}); err != nil {
			t.Fatal(err)
		}
		if len(models.options) != 2 {
			t.Fatal(models.options)
		}
		for _, options := range models.options {
			if options.MaxTokens != 128000 || options.CacheRetention != "none" {
				t.Fatal(options)
			}
		}
		if models.options[0].SessionID == models.options[1].SessionID {
			t.Fatal("summary requests reused routing identity")
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:647
	t.Run("retains per-request retries for non-harness callers", func(t *testing.T) {
		p := generationPreparation([]agent.AgentMessage{user("Summarize this.")}, false)
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("", ai.StopReasonError, "rate limit exceeded"), summaryResponse("recovered summary", ai.StopReasonStop, "")}}
		result, err := Compact(t.Context(), p, models, summaryModel(false, 8192), "", "", &ai.RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 0}, ai.RetryCallbacks{})
		if err != nil || !strings.Contains(result.Summary, "recovered summary") {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:680
	t.Run("returns compaction error results without throwing", func(t *testing.T) {
		p := generationPreparation([]agent.AgentMessage{user("Summarize this.")}, false)
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("", ai.StopReasonError, "history failed")}}
		_, err := Compact(t.Context(), p, models, summaryModel(false, 8192), "", "", nil, ai.RetryCallbacks{})
		assertCompactionFailure(t, err, harness.CompactionErrorSummarizationFailed, "Summarization failed: history failed")
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:710
	t.Run("combines usage for split-turn summaries", func(t *testing.T) {
		messages := []agent.AgentMessage{user("Summarize this.")}
		p := generationPreparation(messages, true)
		p.MessagesToSummarize = messages
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("history summary", ai.StopReasonStop, ""), summaryResponse("turn prefix summary", ai.StopReasonStop, "")}}
		models.responses[0].Usage = *mockUsage(1, 2, 3, 4)
		models.responses[1].Usage = *mockUsage(5, 6, 7, 8)
		result, err := Compact(t.Context(), p, models, summaryModel(false, 8192), "", "", nil, ai.RetryCallbacks{})
		if err != nil || !reflect.DeepEqual(result.Usage, mockUsage(6, 8, 10, 12)) {
			t.Fatalf("usage=%+v error=%v", result.Usage, err)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:736
	t.Run("passes reasoning through turn-prefix summaries when enabled", func(t *testing.T) {
		p := generationPreparation([]agent.AgentMessage{user("Summarize this.")}, true)
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("## Original Request\nTest summary", ai.StopReasonStop, "")}}
		if _, err := Compact(t.Context(), p, models, summaryModel(true, 8192), "", ai.ThinkingHigh, nil, ai.RetryCallbacks{}); err != nil {
			t.Fatal(err)
		}
		if len(models.options) != 1 || models.options[0].Thinking != ai.ThinkingHigh {
			t.Fatal(models.options)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:763
	t.Run("returns turn-prefix errors without throwing", func(t *testing.T) {
		for _, test := range []struct {
			reason        ai.StopReason
			message, want string
			code          harness.CompactionErrorCode
		}{{ai.StopReasonError, "prefix failed", "Turn prefix summarization failed: prefix failed", harness.CompactionErrorSummarizationFailed}, {ai.StopReasonAborted, "prefix stopped", "prefix stopped", harness.CompactionErrorAborted}} {
			models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("", test.reason, test.message)}}
			_, err := Compact(t.Context(), generationPreparation([]agent.AgentMessage{user("Summarize this.")}, true), models, summaryModel(false, 8192), "", "", nil, ai.RetryCallbacks{})
			assertCompactionFailure(t, err, test.code, test.want)
		}
	})
	// .upstream/v0.87.1/packages/agent/test/harness/compaction.test.ts:803
	t.Run("returns compaction result with file details", func(t *testing.T) {
		var f entryFactory
		u1 := f.message(user("read a file"))
		message := assistant("calling tool", mockUsage(1000, 200, 0, 0))
		message.Assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "tool-1", Name: "read", Arguments: ai.JsonObject{"path": "src/index.ts"}}}
		a1 := f.message(message, u1.ID)
		u2 := f.message(user("continue"), a1.ID)
		a2 := f.message(assistant("done", mockUsage(4000, 500, 0, 0)), u2.ID)
		p := preparation(t, []session.Entry{u1, a1, u2, a2}, DefaultCompactionSettings)
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("## Goal\nTest summary", ai.StopReasonStop, "")}}
		result, err := Compact(t.Context(), *p, models, summaryModel(false, 8192), "", "", nil, ai.RetryCallbacks{})
		if err != nil || result.Summary == "" || result.Usage == nil || result.Usage.TotalTokens <= 0 || len(result.RetainedTail) == 0 || result.Details == nil {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	})
}
