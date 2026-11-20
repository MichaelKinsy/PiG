// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// TestCompactionUnknownWindowNoThresholdCompaction sends one prompt to a session whose model reports
// no context window (0) and whose reply carries usage. A zero window is unknown, so the threshold check
// must not compact: no compaction entry, no compaction events, and no summariser request.
// The 1,000,000-window row is the control: the same reply stays far under its threshold.
func TestCompactionUnknownWindowNoThresholdCompaction(t *testing.T) {
	cases := []struct {
		name   string
		window int
	}{
		{"unknown window (0) does not threshold-compact after a reply", 0},
		{"control: 1M window does not threshold-compact after the same reply", 1_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedProvider{}
			reply := func([]ai.Message) *ai.AssistantMessage {
				return &ai.AssistantMessage{
					Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "4"}},
					Provider:   "faux",
					Model:      "faux-1",
					StopReason: ai.StopReasonStop,
					Usage:      ai.Usage{Input: 1000, Output: 10, TotalTokens: 1010},
					Timestamp:  time.Now().UnixMilli(),
				}
			}
			provider.responses = append(provider.responses, reply)
			for range 4 {
				provider.responses = append(provider.responses, fauxReply("complete summary", ai.StopReasonStop, 0))
			}
			model := fakeModelWithProvider(provider)
			model.Capabilities.ContextWindow = tc.window
			model.Capabilities.MaxOutputTokens = 8192
			s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, NoSession: true, SystemPrompt: "You are a helpful assistant. Be concise."})
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Events()
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := s.Send(t.Context(), "What is 2+2? Reply with just the number."); err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, s)
			var compactionEvents int
			for _, event := range events {
				switch event.(type) {
				case agent.CompactionStartEvent, agent.CompactionEndEvent:
					compactionEvents++
				}
			}
			compactions := 0
			for _, entry := range s.inner.Load().GetEntries() {
				if entry.Base().Type == "compaction" {
					compactions++
				}
			}
			if compactions != 0 || compactionEvents != 0 {
				t.Fatalf("threshold compaction ran: entries=%d events=%d requests=%d", compactions, compactionEvents, provider.callCount())
			}
			if provider.callCount() != 1 {
				t.Fatalf("requests=%d, want only the prompt reply (no summariser call)", provider.callCount())
			}
			fmt.Printf("UNKNOWN_WINDOW window=%d compactions=0 requests=1\n", tc.window)
		})
	}
}
