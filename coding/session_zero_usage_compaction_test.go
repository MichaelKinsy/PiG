package coding

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/suite/regressions/8328-zero-usage-auto-compaction.test.ts.
func TestZeroUsageAutoCompactionUsesMessageEstimate(t *testing.T) {
	for _, tc := range []struct {
		name        string
		userText    string
		wantCompact bool
	}{
		{name: "above threshold", userText: strings.Repeat("x", 16_000), wantCompact: true},
		{name: "below threshold", userText: "short", wantCompact: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := fakeModel()
			model.Capabilities.ContextWindow = 20_000
			sess, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := sess.Close(); err != nil {
					t.Error(err)
				}
			}()

			completer := &fakeCompleter{summary: "estimated context summary"}
			sess.completer = completer
			now := time.Now().UnixMilli()
			user := agent.AgentMessage{User: &agent.UserMessage{
				Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: tc.userText}}, Timestamp: now - 1,
			}}
			assistant := &agent.AssistantMessage{
				Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "response"}},
				Provider: model.Provider.ID(), ModelID: model.ID, StopReason: ai.StopReasonStop, Usage: &ai.Usage{}, Timestamp: now,
			}
			for _, message := range []agent.AgentMessage{user, {Assistant: assistant}} {
				if _, err := sess.inner.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			sess.refreshContext()

			if _, err := sess.checkCompaction(context.Background(), assistant, true, nil); err != nil {
				t.Fatal(err)
			}
			if got := completer.called.Load(); got != tc.wantCompact {
				t.Fatalf("compaction called = %v, want %v", got, tc.wantCompact)
			}
		})
	}
}
