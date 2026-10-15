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

// Open findings: differences between PiG and Pi 1.1.0 that the journeys found and that are not fixed in this lane. Each normalizer rewrites PiG's spelling to Pi's so the rest of the frame, request or session is still compared. A normalizer that stops matching fails the strict run (JOURNEY_STRICT_NORMALIZERS=1) and must be deleted with its fix.
var (
	// FINDING e2e-1. After Escape aborts a running tool, Pi's agent loop makes the next provider call with the aborted signal; Node's fetch rejects with "This operation was aborted". PiG's request fails with Go's context error text, shown as "Error: context canceled" and persisted as errorMessage.
	findingAbortedFetchText = harness.Normalizer{
		Pattern: `context canceled`, With: "This operation was aborted", Kinds: []harness.Kind{harness.PiG},
		Reason: "FINDING e2e-1: PiG surfaces Go's \"context canceled\" where Pi's fetch rejects with \"This operation was aborted\" (packages/ai/src/api/openai-completions.ts:719 formatProviderError of the AbortError)",
	}
	// FINDING e2e-2. Pi's interactive message_end handler assigns "Operation aborted" to the aborted assistant message itself (interactive-mode.ts:3563-3570), so the persisted session entry carries it. PiG renders the replacement from a copy and persists the provider's "Request was aborted".
	findingAbortedSessionText = harness.Normalizer{
		Pattern: `"errorMessage":"Request was aborted"`, With: `"errorMessage":"Operation aborted"`, Kinds: []harness.Kind{harness.PiG},
		Reason: "FINDING e2e-2: PiG persists \"Request was aborted\" where Pi's interactive mode mutates the message to \"Operation aborted\" before it is saved (interactive-mode.ts:3563-3570)",
	}
)

// D80: PiG's /settings lists one more row than Pi, "Mask secret input" (docs/parity/DIVERGENCES.md D80), so its position counter ends in /33 where Pi's ends in /32.
var d80SettingsCount = harness.Normalizer{
	Pattern: `\((\d+)/33\)`, With: "(${1}/32)", Kinds: []harness.Kind{harness.PiG},
	Reason: "D80: PiG's /settings has the extra row \"Mask secret input\"; Pi lists 32 rows",
}

// After a branch summary or a failed turn the footer has no provider usage to show and estimates the context from the request text, system prompt included; the two products' system prompts differ in length, so the percentage can differ by a rounding step.
var estimatedFooterPercent = harness.Normalizer{
	Pattern: `\d+\.\d%/128k`, With: "N%/128k",
	Reason: "the footer percentage is estimated from text that includes the system prompt (compaction.ts estimateContextTokens); the products' prompts differ in length",
}
