package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The owner's turn: input 2, cacheRead 294000, cacheWrite 1400, output 860. Pi sums every usage field (calculateContextTokens, compaction.ts:135), so a turn dominated
// by cacheRead is 29.6% of a 1,000,000 window; a model with no window prints Pi's 0.0%/0 (footer.ts:158-160); an unknown estimate, as after a compaction no response has followed,
// prints Pi's "?" with the window (footer.ts:162-165).
func TestFooterContextUsageOfACacheReadDominatedTurn(t *testing.T) {
	usage := &ai.Usage{Input: 2, CacheRead: 294000, CacheWrite: 1400, Output: 860}
	for _, tc := range []struct {
		name   string
		window int
		unknown bool
		want   string
	}{
		{"window known", 1_000_000, false, "29.6%/1.0M (auto)"},
		{"window unknown to the model", 0, false, "0.0%/0 (auto)"},
		{"estimate unknown after a compaction", 1_000_000, true, "?/1.0M (auto)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := NewFooterComponent(&ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: tc.window}}, "", nil)
			status.SetTurnContextUsage(usage)
			if tc.unknown {
				status.SetContextUsage(nil, tc.window)
			}
			if got := stripANSI(strings.Join(status.Render(120), "\n")); !strings.Contains(got, tc.want) {
				t.Fatalf("footer = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
