// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

type compactionDecisionCall struct {
	reason string
	retry  bool
}

func autoQueueSession(t *testing.T, smallKeep bool) *Session {
	t.Helper()
	var services *Services
	if smallKeep {
		services = newTestServicesSmallKeep(t)
	} else {
		services = newTestServices(t)
	}
	model := fakeModel()
	model.Capabilities.ContextWindow = 200000
	model.Capabilities.MaxOutputTokens = 8192
	s, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true, SystemPrompt: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func queueAssistant(s *Session, text string, input, output int, reason ai.StopReason, timestamp int64) *agent.AssistantMessage {
	return &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "anthropic-messages", Provider: s.Model().Provider.ID(), ModelID: s.Model().ID, Usage: &ai.Usage{Input: input, Output: output, TotalTokens: input + output}, StopReason: reason, Timestamp: timestamp}
}
func queueUser(text string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: timestamp}}
}
func persistQueueMessage(t *testing.T, s *Session, message agent.AgentMessage) string {
	t.Helper()
	id, err := s.inner.AppendMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func checkQueueDecision(t *testing.T, s *Session, message *agent.AssistantMessage, skipAborted bool, calls *[]compactionDecisionCall) {
	t.Helper()
	_, err := s.checkCompactionDecision(t.Context(), message, skipAborted, nil, func(_ context.Context, reason string, retry bool) (bool, error) {
		*calls = append(*calls, compactionDecisionCall{reason, retry})
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutoCompactionQueueUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-auto-compaction-queue.test.ts:59
	t.Run("should resume after threshold compaction when only agent-level queued messages exist", func(t *testing.T) {
		s := autoQueueSession(t, true)
		now := time.Now().UnixMilli()
		persistQueueMessage(t, s, queueUser("message to compact", now-1000))
		persistQueueMessage(t, s, agent.AgentMessage{Assistant: queueAssistant(s, "assistant response to compact", 100, 0, ai.StopReasonStop, now-500)})
		s.RefreshContext()
		s.completer = &fakeCompleter{summary: "compacted"}
		s.agent.FollowUp(agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "test", "content": []any{map[string]any{"type": "text", "text": "Queued custom"}}, "display": false, "timestamp": now}})
		if s.PendingMessageCount() != 0 || !s.agent.HasQueuedMessages() {
			t.Fatal("expected only agent-level queue")
		}
		if compacted, err := s.runAutoCompaction(t.Context(), "threshold", false); !compacted || err != nil {
			t.Fatal("compaction failed")
		}
		if !s.agent.HasQueuedMessages() {
			t.Fatal("automatic compaction unexpectedly continued the agent")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-auto-compaction-queue.test.ts:135
	t.Run("should not compact repeatedly after overflow recovery already attempted", func(t *testing.T) {
		s := autoQueueSession(t, false)
		msg := queueAssistant(s, "", 0, 0, ai.StopReasonError, time.Now().UnixMilli())
		msg.ErrorMessage = "prompt is too long"
		var calls []compactionDecisionCall
		checkQueueDecision(t, s, msg, true, &calls)
		msg.Timestamp++
		checkQueueDecision(t, s, msg, true, &calls)
		if len(calls) != 1 {
			t.Fatalf("calls=%v", calls)
		}
		found := false
		for _, event := range drainEvents(t, s) {
			if end, ok := event.(agent.CompactionEndEvent); ok && end.Reason == "overflow" && end.ErrorMessage == "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model." {
				found = true
			}
		}
		if !found {
			t.Fatal("missing overflow recovery failure")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-auto-compaction-queue.test.ts:190
	t.Run("should ignore stale pre-compaction assistant usage on pre-prompt compaction checks", func(t *testing.T) {
		s := autoQueueSession(t, false)
		now := time.Now().UnixMilli()
		msg := queueAssistant(s, "large response before compaction", 600000, 10000, ai.StopReasonStop, now-10000)
		first := persistQueueMessage(t, s, queueUser("before compaction", msg.Timestamp-1000))
		persistQueueMessage(t, s, agent.AgentMessage{Assistant: msg})
		if _, err := s.inner.AppendCompaction("summary", first, 610000, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		persistQueueMessage(t, s, queueUser("session recovery payload", now))
		var calls []compactionDecisionCall
		checkQueueDecision(t, s, msg, false, &calls)
		if len(calls) != 0 {
			t.Fatal(calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-auto-compaction-queue.test.ts:247
	t.Run("should trigger threshold compaction for error messages using last successful usage", func(t *testing.T) {
		s := autoQueueSession(t, false)
		now := time.Now().UnixMilli()
		settings, err := s.compactionSettings()
		if err != nil {
			t.Fatal(err)
		}
		threshold := s.Model().Capabilities.ContextWindow - settings.ReserveTokens + 1
		success := queueAssistant(s, "large successful response", threshold-10000, 10000, ai.StopReasonStop, now)
		failure := queueAssistant(s, "", 0, 0, ai.StopReasonError, now+1000)
		failure.ErrorMessage = "529 overloaded"
		s.agent.SetMessages([]agent.AgentMessage{queueUser("hello", now-1000), {Assistant: success}, queueUser("another prompt", now+500), {Assistant: failure}})
		var calls []compactionDecisionCall
		checkQueueDecision(t, s, failure, true, &calls)
		if len(calls) != 1 || calls[0] != (compactionDecisionCall{"threshold", false}) {
			t.Fatal(calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-auto-compaction-queue.test.ts:320
	t.Run("should not trigger threshold compaction for error messages when no prior usage exists", func(t *testing.T) {
		s := autoQueueSession(t, false)
		now := time.Now().UnixMilli()
		failure := queueAssistant(s, "", 0, 0, ai.StopReasonError, now)
		failure.ErrorMessage = "529 overloaded"
		s.agent.SetMessages([]agent.AgentMessage{queueUser("hello", now-1000), {Assistant: failure}})
		var calls []compactionDecisionCall
		checkQueueDecision(t, s, failure, true, &calls)
		if len(calls) != 0 {
			t.Fatal(calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-auto-compaction-queue.test.ts:368
	t.Run("should not trigger threshold compaction for error messages when only kept pre-compaction usage exists", func(t *testing.T) {
		s := autoQueueSession(t, false)
		now := time.Now().UnixMilli()
		kept := queueAssistant(s, "kept response from before compaction", 180000, 10000, ai.StopReasonStop, now-10000)
		first := persistQueueMessage(t, s, queueUser("before compaction", kept.Timestamp-1000))
		persistQueueMessage(t, s, agent.AgentMessage{Assistant: kept})
		if _, err := s.inner.AppendCompaction("summary", first, 190000, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		failure := queueAssistant(s, "", 0, 0, ai.StopReasonError, now)
		failure.ErrorMessage = "529 overloaded"
		s.agent.SetMessages([]agent.AgentMessage{queueUser("kept user msg", kept.Timestamp-1000), {Assistant: kept}, queueUser("new prompt", now-500), {Assistant: failure}})
		var calls []compactionDecisionCall
		checkQueueDecision(t, s, failure, true, &calls)
		if len(calls) != 0 {
			t.Fatal(calls)
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/pre-prompt-compaction-no-continue.test.ts:26
func TestPrePromptCompactionDoesNotContinueFromAssistant(t *testing.T) {
	s := newTreeTestSessionWithSettings(t, `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`)
	reply := fauxReply("answered next prompt", ai.StopReasonStop, 0)(nil)
	provider := &scriptedProvider{responses: []scriptedResponse{func([]ai.Message) *ai.AssistantMessage { return reply }}}
	model := fakeModelWithProvider(provider)
	model.ID = "faux-1"
	model.Capabilities.ContextWindow = 100
	model.Capabilities.MaxOutputTokens = 100
	s.agent.SetModel(model)
	// The extension result avoids a second provider request during summarization.
	withTreeHandlers(s, t, summaryOverrideHandlers("pre-prompt summary", nil))
	now := time.Now().UnixMilli()
	persistQueueMessage(t, s, queueUser("previous prompt", now-1000))
	persistQueueMessage(t, s, agent.AgentMessage{Assistant: queueAssistant(s, "length-stop assistant response", 100, 0, ai.StopReasonLength, now-500)})
	s.RefreshContext()
	if _, err := s.Send(t.Context(), "next prompt"); err != nil {
		t.Fatal(err)
	}
	var end *agent.CompactionEndEvent
	for _, event := range drainEvents(t, s) {
		if value, ok := event.(agent.CompactionEndEvent); ok {
			end = &value
		}
	}
	if end == nil || end.Reason != "overflow" || end.Aborted || !end.WillRetry {
		t.Fatalf("end=%+v", end)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls=%d", provider.callCount())
	}
	provider.mu.Lock()
	request := provider.requests[0]
	provider.mu.Unlock()
	if !strings.Contains(request, "next prompt") {
		t.Fatal("new prompt not delivered")
	}
}

func BenchmarkCheckCompactionDecision(b *testing.B) {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := NewServices(ServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	model := fakeModel()
	model.Capabilities.ContextWindow = 200000
	s, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true, SystemPrompt: "Test"})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Error(err)
		}
	})
	message := queueAssistant(s, "complete", 100, 10, ai.StopReasonStop, time.Now().UnixMilli())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.checkCompaction(b.Context(), message, true, nil); err != nil {
			b.Fatal(err)
		}
	}
}
