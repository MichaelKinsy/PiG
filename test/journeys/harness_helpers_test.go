//go:build parity

package journeys

import (
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

// summarizing matches the context-summarization requests of compaction and branch summaries: Pi sends them with SUMMARIZATION_SYSTEM_PROMPT (core/compaction/utils.ts).
func summarizing(r *harness.Request) bool {
	return len(r.Messages) > 0 && strings.Contains(r.Messages[0].Text, "context summarization assistant")
}

// onSummary answers summarization requests.
func onSummary(reply func(r *harness.Request) harness.Reply) rule {
	return func(r *harness.Request) (harness.Reply, bool) {
		if summarizing(r) {
			return reply(r), true
		}
		return harness.Reply{}, false
	}
}

// prompt submits text and waits until the provider has answered base+requests requests and the UI is idle.
func prompt(c *harness.Ctx, text string, requests int) {
	base := len(c.P.Requests())
	c.Say(text)
	c.Idle(base + requests)
}

// After a branch summary or a failed turn the footer has no provider usage to show and estimates the context from the request text, system prompt included; the two products' system prompts differ in length, so the percentage can differ by a rounding step.
var estimatedFooterPercent = harness.Normalizer{
	Pattern: `\d+\.\d%/128k`, With: "N%/128k",
	Reason: "the footer percentage is estimated from text that includes the system prompt (compaction.ts estimateContextTokens); the products' prompts differ in length",
}
