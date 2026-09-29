// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func retryCompactionSession(t *testing.T, enabled bool, maxRetries, delay int, script []string) (*Session, *scriptedProvider) {
	t.Helper()
	s := newTreeTestSessionWithSettings(t, fmt.Sprintf(`{"compaction":{"keepRecentTokens":1},"retry":{"enabled":%t,"maxRetries":%d,"baseDelayMs":%d}}`, enabled, maxRetries, delay))
	p := &scriptedProvider{}
	for _, text := range script {
		p.responses = append(p.responses, func([]ai.Message) *ai.AssistantMessage {
			reason := ai.StopReasonError
			content := ""
			errorText := text
			if text == "" {
				reason = ai.StopReasonStop
				content = "recovered summary"
				errorText = ""
			}
			return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", Content: []ai.AssistantContentBlock{ai.TextContent{Text: content}}, Usage: ai.Usage{Input: 10, TotalTokens: 10}, StopReason: reason, ErrorMessage: errorText, Timestamp: time.Now().UnixMilli()}
		})
	}
	model := fakeModelWithProvider(p)
	model.ID = "faux-1"
	model.Capabilities.ContextWindow = 200000
	s.agent.SetModel(model)
	now := time.Now().UnixMilli()
	persistQueueMessage(t, s, queueUser("message to compact", now-1000))
	persistQueueMessage(t, s, agent.AgentMessage{Assistant: queueAssistant(s, "assistant response to compact", 100, 0, ai.StopReasonStop, now-500)})
	s.RefreshContext()
	return s, p
}

func TestCompactionRetriesTransientStreamDropUpstream(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		enabled                    bool
		max                        int
		script                     []string
		calls, schedules, finished int
		failure                    string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6647-compaction-retries-transient-stream-drop.test.ts:82
		{"retries a transient terminated summarization error and compacts successfully", true, 3, []string{"terminated", "terminated", ""}, 3, 2, 1, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6647-compaction-retries-transient-stream-drop.test.ts:114
		{"does not retry a non-retryable error insufficient_quota", true, 3, []string{"insufficient_quota"}, 1, 0, 0, "insufficient_quota"},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6647-compaction-retries-transient-stream-drop.test.ts:131
		{"does not retry when retry is disabled", false, 3, []string{"terminated"}, 1, 0, 0, "terminated"},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6647-compaction-retries-transient-stream-drop.test.ts:148
		{"stops retrying after maxRetries and reports failure", true, 2, []string{"terminated", "terminated", "terminated"}, 3, 2, 1, "terminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := retryCompactionSession(t, tc.enabled, tc.max, 0, tc.script)
			result, err := s.CompactResult(t.Context(), "")
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("error=%v", err)
				}
			} else {
				if err != nil || !strings.Contains(result.Summary, "recovered summary") {
					t.Fatalf("result=%+v error=%v", result, err)
				}
			}
			if p.callCount() != tc.calls {
				t.Fatalf("calls=%d want=%d", p.callCount(), tc.calls)
			}
			var scheduled []agent.SummarizationRetryScheduledEvent
			finished := 0
			for _, event := range drainEvents(t, s) {
				switch e := event.(type) {
				case agent.SummarizationRetryScheduledEvent:
					scheduled = append(scheduled, e)
				case agent.SummarizationRetryFinishedEvent:
					finished++
				}
			}
			if len(scheduled) != tc.schedules || finished != tc.finished {
				t.Fatalf("scheduled=%v finished=%d", scheduled, finished)
			}
			for i, e := range scheduled {
				if e.Attempt != i+1 || e.MaxAttempts != tc.max || e.ErrorMessage != "terminated" {
					t.Fatal(e)
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6647-compaction-retries-transient-stream-drop.test.ts:169
	t.Run("aborts an in-flight retry backoff via abortCompaction", func(t *testing.T) {
		s, p := retryCompactionSession(t, true, 5, 30000, []string{"terminated", "terminated", "terminated"})
		started := make(chan struct{}, 1)
		s.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.SummarizationRetryScheduledEvent); ok {
				started <- struct{}{}
			}
		})
		done := make(chan error, 1)
		go func() { done <- s.Compact(t.Context(), "") }()
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("ended before backoff: %v", err)
		}
		s.AbortCompaction()
		if err := <-done; err == nil {
			t.Fatal("cancelled compaction succeeded")
		}
		var end *agent.CompactionEndEvent
		for _, event := range drainEvents(t, s) {
			if value, ok := event.(agent.CompactionEndEvent); ok {
				end = &value
			}
		}
		if end == nil || !end.Aborted || p.callCount() != 1 {
			t.Fatalf("end=%+v calls=%d", end, p.callCount())
		}
	})
}
