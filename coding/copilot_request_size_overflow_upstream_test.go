// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/suite/regressions/copilot-request-size-overflow.test.ts (Pi 73f8de80): a real HTTP 413 from the
// request-size limit is context overflow, so the agent compacts an image-heavy history and retries instead of dying on the error.
// The harness provider is the faux provider whatever Pi's test names it: the classifier is provider-agnostic.
func TestCopilotRequestSizeOverflowRecoveryUpstream(t *testing.T) {
	const (
		copilot413   = `github-copilot API error (413): {"message":"failed to parse request","code":""}`
		requestLimit = 32 * 1024 * 1024
		recentPrompt = "Continue the task using the recent notes."
	)
	requestBytes := func(request []ai.Message) int {
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return len(data)
	}
	var retried string
	h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 1_000_000},
		func(request []ai.Message) *ai.AssistantMessage {
			if got := requestBytes(request); got <= requestLimit {
				t.Errorf("first request is %d bytes, want more than %d", got, requestLimit)
			}
			// Zero usage, as in the affected session.
			return fauxError(copilot413)(request)
		},
		// The overflow recovery summarization runs on the compacted, image-free context.
		fauxReply("The old images have been reviewed. Continue with the recent task.", ai.StopReasonStop, 0),
		func(request []ai.Message) *ai.AssistantMessage {
			data, _ := json.Marshal(request)
			if strings.Contains(string(data), `"type":"image"`) {
				t.Error("the retried request still carries the images")
			}
			retried = "seen"
			return fauxReply("Continued after compaction", ai.StopReasonStop, 0)(request)
		},
	)
	s := h.session
	// Synthetic base64 only; no image decoding or network calls are involved.
	imageData := strings.Repeat("A", 384*1024)
	timestamp := time.Now().UnixMilli() - 10_000
	assistant := func(text string, blocks ...ai.AssistantContentBlock) agent.AgentMessage {
		message := queueAssistant(s, text, 0, 0, ai.StopReasonStop, timestamp)
		if len(blocks) > 0 {
			message.Content, message.StopReason = blocks, ai.StopReasonToolUse
		}
		return agent.AgentMessage{Assistant: message}
	}
	images := make([]ai.ToolResultMessageContent, 0, 89)
	for range 89 {
		images = append(images, ai.ImageContent{MimeType: "image/png", Data: imageData})
	}
	persistQueueMessage(t, s, queueUser("Review the old images", timestamp))
	persistQueueMessage(t, s, assistant("", ai.ToolCall{ID: "read-images", Name: "read", Arguments: map[string]any{"path": "old-images"}}))
	persistQueueMessage(t, s, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "read-images", ToolName: "read", Content: images, Timestamp: timestamp}})
	persistQueueMessage(t, s, assistant("Finished reviewing the old images"))
	// Keep a completed recent turn so recovery does not summarize the entire current request when it omits the failed attempt.
	// The old images are outside the default 20k-token retained tail.
	persistQueueMessage(t, s, queueUser("Recent task notes: "+strings.Repeat("x", 80_000), timestamp))
	persistQueueMessage(t, s, assistant("Ready to continue"))
	s.RefreshContext()

	var starts, ends []agent.AgentEvent
	unsubscribe := s.Subscribe(func(event agent.AgentEvent) {
		switch event.(type) {
		case agent.CompactionStartEvent:
			starts = append(starts, event)
		case agent.CompactionEndEvent:
			ends = append(ends, event)
		}
	})
	defer unsubscribe()

	if _, err := s.Send(t.Context(), recentPrompt); err != nil {
		t.Fatal(err)
	}

	if h.provider.callCount() != 3 || retried == "" {
		t.Fatalf("model calls = %d, retried request seen = %q, want 3 and a retry", h.provider.callCount(), retried)
	}
	if len(starts) != 1 || starts[0].(agent.CompactionStartEvent).Reason != "overflow" {
		t.Fatalf("compaction_start events = %+v", starts)
	}
	if end := ends[len(ends)-1].(agent.CompactionEndEvent); end.Reason != "overflow" || end.Aborted || !end.WillRetry {
		t.Fatalf("compaction_end = %+v", end)
	}
	// The session continued to a final answer instead of dying on the 413.
	if got := suiteLastText(s); got != "Continued after compaction" {
		t.Fatalf("last assistant text = %q", got)
	}
}
