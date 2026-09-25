package compaction

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// compaction.ts estimateTokens counts JSON.stringify(arguments), which leaves
// U+2028 literal.
func TestEstimateTokensCountsJSONStringifyLength(t *testing.T) {
	separators := strings.Repeat(string(rune(0x2028)), 4)
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ToolCall{Name: "f", Arguments: ai.JsonObject{"x": separators}},
	}}}
	// f plus the 12-unit JSON text is 13 units, 4 tokens.
	if got := EstimateTokens(message); got != 4 {
		t.Fatalf("tokens = %d, want 4", got)
	}
}
