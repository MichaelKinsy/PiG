package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi cache-stats.ts counts misses strictly above 1024 tokens and only after
// cache activity; validate the persisted-message accounting consumer.
func TestCodexCacheWasteBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                string
		cached, input, want int
	}{
		{"noise floor", 1024, 1024, 0},
		{"above noise", 1025, 1025, 1025},
		{"shrunk prompt", 5000, 2000, 2000},
		{"grown prompt", 2000, 5000, 2000},
		{"provider never caches", 0, 5000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := NewSession("cache-boundary", t.TempDir())
			prior := &ai.Usage{CacheRead: tc.cached}
			if tc.cached == 0 {
				prior.Input = 5000
			}
			for _, usage := range []*ai.Usage{prior, {Input: tc.input}} {
				if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
					Role: "assistant", Provider: "unit-provider", ModelID: "unit-model", Usage: usage,
				}}); err != nil {
					t.Fatal(err)
				}
			}
			waste := session.Accounting().CacheWaste
			if waste.MissedTokens != tc.want {
				t.Fatalf("missed tokens = %d, want %d", waste.MissedTokens, tc.want)
			}
			wantCount := 0
			if tc.want > 0 {
				wantCount = 1
			}
			if waste.MissCount != wantCount {
				t.Fatalf("miss count = %d, want %d", waste.MissCount, wantCount)
			}
		})
	}
}
