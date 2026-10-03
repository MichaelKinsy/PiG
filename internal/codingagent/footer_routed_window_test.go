package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream 0.99.1 footer.ts:162 takes the context window from session.getContextUsage(), which measures against _limitsModel (agent-session.ts:4139-4144): under a virtual selection, the physical model that answered last.
func TestFooterContextWindowFollowsTheRoutedModel(t *testing.T) {
	footer := upstreamFooter(t, nil, "router", "auto")
	footer.model.Capabilities.ContextWindow = 1_000_000
	footer.contextTokens = 50_000
	physical := footerTestModel("faux", "large", 0, 0)
	footer.SetRoutedModelSource(func() *RoutedModelSelection {
		return &RoutedModelSelection{Model: physical, ThinkingLevel: ai.ThinkingLevel("medium")}
	})
	// 50000 / 200000 of the routed model, not 5.0%/1.0M of the virtual selection.
	assertUpstreamFooterStats(t, footer, "25.0%/200k")

	// Without a routed response, the selected model's window applies.
	footer.SetRoutedModelSource(func() *RoutedModelSelection { return nil })
	assertUpstreamFooterStats(t, footer, "5.0%/1.0M")
}
