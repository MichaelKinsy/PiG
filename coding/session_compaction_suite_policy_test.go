// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func suitePolicySession(t *testing.T, window int, settings string) *Session {
	t.Helper()
	if window == 0 {
		window = 200000
	}
	s := autoQueueSession(t, false)
	model := *s.Model()
	model.Capabilities.ContextWindow = window
	if window == 100 {
		model.Capabilities.MaxOutputTokens = 100
	}
	s.agent.SetModel(&model)
	if settings != "" {
		var parsed icodingagent.Settings
		if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
			t.Fatal(err)
		}
		s.services.SettingsManager().ApplyOverrides(parsed)
	}
	return s
}
func suitePolicyCheck(t *testing.T, s *Session, message *agent.AssistantMessage, skip bool, calls *[]compactionDecisionCall) {
	t.Helper()
	_, err := s.checkCompactionDecision(t.Context(), message, skip, nil, func(_ context.Context, reason string, retry bool) (bool, error) {
		*calls = append(*calls, compactionDecisionCall{reason, retry})
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func suiteAssertRecoveryError(t *testing.T, s *Session, want string) {
	t.Helper()
	for _, event := range drainEvents(t, s) {
		if end, ok := event.(agent.CompactionEndEvent); ok && end.ErrorMessage == want {
			return
		}
	}
	t.Fatal("missing recovery error: " + want)
}

func TestAgentSessionCompactionSuitePolicyUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:705
	t.Run("keeps overflow wording when repeated length stop fills context window", func(t *testing.T) {
		s := suitePolicySession(t, 100, "")
		message := queueAssistant(s, "", 100, 0, ai.StopReasonLength, time.Now().UnixMilli())
		var calls []compactionDecisionCall
		suitePolicyCheck(t, s, message, true, &calls)
		message.Timestamp++
		suitePolicyCheck(t, s, message, true, &calls)
		if len(calls) != 1 {
			t.Fatal(calls)
		}
		suiteAssertRecoveryError(t, s, "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model.")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:803
	t.Run("does not retry overflow recovery more than once", func(t *testing.T) {
		s := suitePolicySession(t, 0, "")
		message := queueAssistant(s, "", 0, 0, ai.StopReasonError, time.Now().UnixMilli())
		message.ErrorMessage = "prompt is too long"
		var calls []compactionDecisionCall
		suitePolicyCheck(t, s, message, true, &calls)
		message.Timestamp++
		suitePolicyCheck(t, s, message, true, &calls)
		if len(calls) != 1 {
			t.Fatal(calls)
		}
		suiteAssertRecoveryError(t, s, "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model.")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:860
	t.Run("ignores stale pre-compaction assistant usage on pre-prompt checks", func(t *testing.T) {
		s := suitePolicySession(t, 0, "")
		now := time.Now().UnixMilli()
		assistant := queueAssistant(s, "", 610000, 0, ai.StopReasonStop, now-10000)
		first := persistQueueMessage(t, s, queueUser("before compaction", assistant.Timestamp-1000))
		persistQueueMessage(t, s, agent.AgentMessage{Assistant: assistant})
		if _, err := s.inner.AppendCompaction("summary", first, assistant.Usage.TotalTokens, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		persistQueueMessage(t, s, queueUser("after compaction", now))
		var calls []compactionDecisionCall
		suitePolicyCheck(t, s, assistant, false, &calls)
		if len(calls) != 0 {
			t.Fatal(calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:898
	t.Run("triggers threshold compaction for error messages using last successful usage", func(t *testing.T) {
		s := suitePolicySession(t, 0, "")
		now := time.Now().UnixMilli()
		success := queueAssistant(s, "", 190000, 0, ai.StopReasonStop, now)
		failure := queueAssistant(s, "", 0, 0, ai.StopReasonError, now+1000)
		failure.ErrorMessage = "529 overloaded"
		s.agent.SetMessages([]agent.AgentMessage{queueUser("hello", now-1000), {Assistant: success}, queueUser("retry", now+500), {Assistant: failure}})
		var calls []compactionDecisionCall
		suitePolicyCheck(t, s, failure, true, &calls)
		if !reflect.DeepEqual(calls, []compactionDecisionCall{{"threshold", false}}) {
			t.Fatal(calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:926
	t.Run("does not trigger threshold compaction for error messages when no prior usage exists", func(t *testing.T) {
		s := suitePolicySession(t, 0, "")
		now := time.Now().UnixMilli()
		failure := queueAssistant(s, "", 0, 0, ai.StopReasonError, now)
		failure.ErrorMessage = "529 overloaded"
		s.agent.SetMessages([]agent.AgentMessage{queueUser("hello", now-1000), {Assistant: failure}})
		var calls []compactionDecisionCall
		suitePolicyCheck(t, s, failure, true, &calls)
		if len(calls) != 0 {
			t.Fatal(calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:947
	t.Run("does not trigger threshold compaction when only kept pre-compaction usage exists", func(t *testing.T) {
		s := suitePolicySession(t, 0, "")
		now := time.Now().UnixMilli()
		kept := queueAssistant(s, "", 190000, 0, ai.StopReasonStop, now-10000)
		first := persistQueueMessage(t, s, queueUser("before compaction", kept.Timestamp-1000))
		persistQueueMessage(t, s, agent.AgentMessage{Assistant: kept})
		if _, err := s.inner.AppendCompaction("summary", first, kept.Usage.TotalTokens, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		failure := queueAssistant(s, "", 0, 0, ai.StopReasonError, now)
		failure.ErrorMessage = "529 overloaded"
		s.agent.SetMessages([]agent.AgentMessage{queueUser("kept user", kept.Timestamp-1000), {Assistant: kept}, queueUser("new prompt", now-500), {Assistant: failure}})
		var calls []compactionDecisionCall
		suitePolicyCheck(t, s, failure, true, &calls)
		if len(calls) != 0 {
			t.Fatal(calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:992
	t.Run("does not trigger threshold compaction below threshold or when disabled", func(t *testing.T) {
		below := suitePolicySession(t, 200000, `{"compaction":{"enabled":true,"reserveTokens":1000}}`)
		disabled := suitePolicySession(t, 0, `{"compaction":{"enabled":false}}`)
		var belowCalls, disabledCalls []compactionDecisionCall
		suitePolicyCheck(t, below, queueAssistant(below, "", 1000, 0, ai.StopReasonStop, time.Now().UnixMilli()), true, &belowCalls)
		suitePolicyCheck(t, disabled, queueAssistant(disabled, "", 1000000, 0, ai.StopReasonStop, time.Now().UnixMilli()), true, &disabledCalls)
		if len(belowCalls) != 0 || len(disabledCalls) != 0 {
			t.Fatalf("below=%v disabled=%v", belowCalls, disabledCalls)
		}
	})
}

func TestAgentSessionCompactionSuiteRecoveryUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:440
	t.Run("compacts and resumes after length stop below desired output limit", func(t *testing.T) {
		h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 1000, maxTokens: 100, settings: `{"compaction":{"keepRecentTokens":1,"reserveTokens":0}}`, extension: suiteSummaryExtension("overflow compacted", nil)}, fauxReply("partial response", ai.StopReasonLength, 0), fauxReply("completed response", ai.StopReasonStop, 0))
		if _, err := h.session.Send(t.Context(), strings.Repeat("x", 5000)); err != nil {
			t.Fatal(err)
		}
		events := h.settle(t)
		var end agent.CompactionEndEvent
		for _, event := range events {
			if value, ok := event.(agent.CompactionEndEvent); ok {
				end = value
			}
		}
		if h.provider.callCount() != 2 || end.Reason != "overflow" || end.Aborted || !end.WillRetry || suiteLastText(h.session) != "completed response" {
			t.Fatalf("requests=%d end=%+v text=%s", h.provider.callCount(), end, suiteLastText(h.session))
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:660
	t.Run("does not compact when length stop reaches desired output limit", func(t *testing.T) {
		h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 1000000, maxTokens: 100}, fauxReply(strings.Repeat("x", 400), ai.StopReasonLength, 0))
		if _, err := h.session.Send(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
		for _, event := range h.settle(t) {
			if _, ok := event.(agent.CompactionStartEvent); ok {
				t.Fatal("unexpected compaction")
			}
		}
		if h.provider.callCount() != 1 {
			t.Fatal(h.provider.callCount())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:673
	t.Run("stops after one compact-and-retry when second response is also truncated", func(t *testing.T) {
		h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 1000000, maxTokens: 100, settings: `{"compaction":{"keepRecentTokens":1,"reserveTokens":0}}`, extension: suiteSummaryExtension("overflow compacted", nil)}, fauxReply(strings.Repeat("x", 64), ai.StopReasonLength, 10*time.Second), fauxReply(strings.Repeat("y", 64), ai.StopReasonLength, 10*time.Second))
		if _, err := h.session.Send(t.Context(), strings.Repeat("x", 5000)); err != nil {
			t.Fatal(err)
		}
		starts := 0
		var end agent.CompactionEndEvent
		for _, event := range h.settle(t) {
			switch value := event.(type) {
			case agent.CompactionStartEvent:
				if value.Reason == "overflow" {
					starts++
				}
			case agent.CompactionEndEvent:
				end = value
			}
		}
		if h.provider.callCount() != 2 || starts != 1 || end.ErrorMessage != "Truncated response recovery failed after one compact-and-retry attempt." {
			t.Fatalf("requests=%d starts=%d end=%+v", h.provider.callCount(), starts, end)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:829
	t.Run("compacts successful overflow responses without retrying", func(t *testing.T) {
		h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 1, maxTokens: 100, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`, extension: suiteSummaryExtension("successful overflow compacted", nil)}, fauxReply("completed answer", ai.StopReasonStop, 0))
		if _, err := h.session.Send(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
		var end agent.CompactionEndEvent
		for _, event := range h.settle(t) {
			if value, ok := event.(agent.CompactionEndEvent); ok {
				end = value
			}
		}
		if h.provider.callCount() != 1 || end.Reason != "overflow" || end.Aborted || end.WillRetry {
			t.Fatalf("requests=%d end=%+v", h.provider.callCount(), end)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:768
	t.Run("resumes after threshold compaction when only agent-level queued messages exist", func(t *testing.T) {
		h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 200000, settings: `{"compaction":{"keepRecentTokens":1}}`, extension: suiteSummaryExtension("auto compacted", nil)}, fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0))
		for _, prompt := range []string{"first", "second"} {
			if _, err := h.session.Send(t.Context(), prompt); err != nil {
				t.Fatal(err)
			}
		}
		h.session.agent.FollowUp(agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "test", "content": []any{map[string]any{"type": "text", "text": "queued custom"}}, "display": false, "timestamp": time.Now().UnixMilli()}})
		compacted, err := h.session.runAutoCompaction(t.Context(), "threshold", false)
		if err != nil || !compacted {
			t.Fatalf("compacted=%v err=%v", compacted, err)
		}
	})
}
