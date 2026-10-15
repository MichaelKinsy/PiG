//go:build parity

package journeys

import (
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

const (
	serverError = `{"error":{"message":"Internal server error","type":"server_error"}}`
	rateLimited = `{"error":{"message":"Rate limit reached for requests","type":"rate_limit_error"}}`
	badRequest  = `{"error":{"message":"Invalid request: unsupported parameter","type":"invalid_request_error"}}`
	overflow    = `{"error":{"message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 200000 tokens. Please reduce the length of the messages.","type":"invalid_request_error","code":"context_length_exceeded"}}`
)

// After a context overflow the failed turn has no usage, so the compaction entry, its banner and the footer estimate the context from the request text, system prompt included, and the two products' system prompts differ in length.
var estimatedContext = []harness.Normalizer{
	{Pattern: `Compacted from [\d,]+ tokens`, With: "Compacted from N tokens", Reason: "tokensBefore is estimated from the system prompt text too (compaction.ts estimateContextTokens); the products' prompts differ in length"},
	{Pattern: `"tokensBefore":\d+`, With: `"tokensBefore":N`, Reason: "same estimate, persisted in the compaction entry"},
	{Pattern: `\d+\.\d%/128k`, With: "N%/128k", Reason: "the footer percentage is estimated from the same text after a compaction"},
}

// The bug-report hint names the running product.
var bugReportHint = harness.Normalizer{
	Pattern: `If this looks like a pig bug`, With: "If this looks like a pi bug", Kinds: []harness.Kind{harness.PiG},
	Reason: "the hint names the product (D2, product name)",
}

func init() {
	register(&harness.Journey{
		Name:        "queue-retry-errors",
		Normalizers: append([]harness.Normalizer{bugReportHint}, estimatedContext...),
		Doc:         "Messages typed while the agent streams (steering with Enter, follow-up with Alt+Enter, Alt+Up to take a queued message back), automatic retry of server and rate-limit errors with the backoff budget from settings, a non-retryable error, an exhausted retry budget, and context overflow answered by automatic compaction (agent-session.ts _isRetryableError, _prepareRetry, overflow recovery).",
		Touches:     []string{"internal/codingagent/", "coding/", "agent/", "ai/", "tui/"},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{Settings: map[string]any{"retry": map[string]any{"enabled": true, "maxRetries": 2, "baseDelayMs": 50},
				"compaction": map[string]any{"enabled": true, "reserveTokens": 16384, "keepRecentTokens": 60}}}
		},
		Script: script(
			onSummary(func(*harness.Request) harness.Reply { return harness.Say("## Goal\nKeep working after the overflow.") }),
			onPrompt("STEER", fixed(harness.Say("Working on the first part. ").Then(harness.Hold("steer")).Then(harness.Say("First part finished.")))),
			onPrompt("FOLLOW", fixed(harness.Say("Working on the main task. ").Then(harness.Hold("follow")).Then(harness.Say("Main task finished.")))),
			onPrompt("DEQUEUE", fixed(harness.Say("Working on something else. ").Then(harness.Hold("dequeue")).Then(harness.Say("Finished without the queued message.")))),
		),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("startup") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "steer", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("STEER first")
				c.WaitGate("steer")
				c.WaitFor("Working on the first part.")
				c.Say("also change direction")
				c.WaitFor("also change direction")
				c.Quiet(c.Settle())
				c.Snap("steering-queued")
				c.P.Release("steer")
				c.Idle(base + 2)
				c.WaitFor("Echo: also change direction")
				c.Snap("steered")
			}}},
			{Step: harness.Step{Name: "follow-up", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("FOLLOW first")
				c.WaitGate("follow")
				c.WaitFor("Working on the main task.")
				c.Type("then summarize it")
				c.Key("M-Enter")
				c.WaitFor("Follow-up")
				c.Quiet(c.Settle())
				c.Snap("follow-up-queued")
				c.P.Release("follow")
				c.Idle(base + 2)
				c.WaitFor("Echo: then summarize it")
				c.Snap("followed-up")
			}}},
			{Step: harness.Step{Name: "dequeue", Run: func(c *harness.Ctx) {
				base := len(c.P.Requests())
				c.Say("DEQUEUE first")
				c.WaitGate("dequeue")
				c.WaitFor("Working on something else.")
				c.Type("never sent")
				c.Key("M-Enter")
				c.WaitFor("Follow-up")
				c.Quiet(c.Settle())
				c.Key("M-Up")
				c.WaitFor("Restored")
				c.Quiet(c.Settle())
				c.Snap("dequeued")
				c.Key("C-u")
				c.P.Release("dequeue")
				c.Idle(base + 1)
				c.WaitFor("Finished without the queued message.")
				c.Quiet(c.Settle())
				c.Snap("finished")
			}}},
			{Step: harness.Step{Name: "retry-server-error", Run: func(c *harness.Ctx) {
				c.P.FailNext(2, 500, serverError)
				prompt(c, "RETRY after server errors", 3)
				c.WaitFor("Echo: RETRY after server errors")
				c.Quiet(c.Settle())
				c.Snap("retried")
			}}},
			{Step: harness.Step{Name: "retry-rate-limit", Run: func(c *harness.Ctx) {
				c.P.FailNext(1, 429, rateLimited)
				prompt(c, "RATE limited once", 2)
				c.WaitFor("Echo: RATE limited once")
				c.Quiet(c.Settle())
				c.Snap("rate-limit-retried")
			}}},
			{Step: harness.Step{Name: "retry-exhausted", Run: func(c *harness.Ctx) {
				c.P.FailNext(3, 500, serverError)
				prompt(c, "EXHAUST the budget", 3)
				c.WaitFor("Retry failed after")
				c.Quiet(c.Settle())
				c.Snap("retry-exhausted")
			}}},
			{Step: harness.Step{Name: "non-retryable", Run: func(c *harness.Ctx) {
				c.P.FailNext(1, 400, badRequest)
				prompt(c, "BAD request", 1)
				c.WaitFor("Invalid request")
				c.Quiet(c.Settle())
				c.Snap("non-retryable")
			}}},
			{Step: harness.Step{Name: "context-overflow", Run: func(c *harness.Ctx) {
				prompt(c, "filler one for the overflow "+strings.Repeat("alpha beta gamma delta ", 20), 1)
				prompt(c, "filler two for the overflow "+strings.Repeat("epsilon zeta eta theta ", 20), 1)
				c.P.FailNext(1, 400, overflow)
				base := len(c.P.Requests())
				c.Say("OVERFLOW now")
				c.Idle(base + 3)
				c.WaitFor("Echo: OVERFLOW now")
				c.Quiet(c.Settle())
				c.Snap("overflow-recovered")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
