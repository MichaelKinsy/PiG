package runtime

import (
	"math"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/src/runtime/conversation-stats.ts: numbers about a conversation's active context, which the worker
// keeps: the estimated context size and the latest response's usage (from the entries its view's window appends), and
// each response's cost (from Durable's `Conversation.context()`). A client holds only a window of the transcript
// (`Chat.view`), so it gets them from the worker (`Chat.stats`, `ChatCosts`).

// AssistantCost is one response's cost.
type AssistantCost struct {
	EntryID   string
	Timestamp int64
	Cost      float64
	// CacheHit is nil when the response reported no prompt tokens.
	CacheHit   *float64
	Provider   string
	Model      string
	StopReason string
}

// CacheHitRate is the share of prompt tokens served from the provider cache, or nil without prompt tokens.
func CacheHitRate(usage ai.Usage) *float64 {
	prompt := usage.Input + usage.CacheRead + usage.CacheWrite
	if prompt <= 0 {
		return nil
	}
	rate := float64(usage.CacheRead) / float64(prompt)
	return &rate
}

// ActiveStats are the numbers of a conversation's active context.
type ActiveStats struct {
	// ContextTokens is the estimated size of the active context, not cumulative usage across requests.
	ContextTokens int
	// LatestAssistantUsage is the last committed assistant response of the active range, never session-wide totals or live
	// partials; nil before one.
	LatestAssistantUsage *ai.Usage
}

// ContextTally keeps the active context's numbers up to date from the entries appended to it: pi-ai's estimate (the
// latest response's reported usage, plus an estimate of what follows it) and the latest response's usage. A whole context
// (`Conversation.context()`, a read of every active entry) is needed only when the active range changes in another way.
// It is not safe for concurrent use.
type ContextTally struct {
	// usage is the context tokens the latest counted response reported, 0 before one.
	usage int
	// trailing is the estimated tokens of the messages after it.
	trailing int
	latest   *ai.Usage
	// seen is the newest entry ID taken.
	seen float64
}

// NewContextTally is an empty tally: reset it from a whole context before advancing.
func NewContextTally() *ContextTally { return &ContextTally{seen: math.Inf(-1)} }

// Stats are the numbers now.
func (t *ContextTally) Stats() ActiveStats {
	return ActiveStats{ContextTokens: t.usage + t.trailing, LatestAssistantUsage: t.latest}
}

// Reset starts over from a whole active context.
func (t *ContextTally) Reset(context durable.ContextView) {
	estimate := ai.EstimateContextTokens(context.Messages)
	t.usage = estimate.UsageTokens
	t.trailing = estimate.TrailingTokens
	t.latest = nil
	t.seen = math.Inf(-1)
	for _, entry := range context.Entries {
		if len(entry.Model) > 0 {
			if message, ok := assistant(entry.Model[0]); ok {
				usage := message.Usage
				t.latest = &usage
			}
		}
		t.seen = math.Max(t.seen, float64(entry.Id))
	}
}

// Advance takes the entries of a view's window newer than the newest taken. It reports false when they do not simply
// append to the active context, so a whole one is needed: the window skipped entries (`before` past the newest taken), or
// one starts a new head (a compaction summary, a reset) or edits earlier entries.
func (t *ContextTally) Advance(view harness.ConversationView) bool {
	if view.Before != nil && float64(*view.Before) > t.seen {
		return false
	}
	first := len(view.Entries)
	for first > 0 && float64(view.Entries[first-1].Id) > t.seen {
		first--
	}
	for _, entry := range view.Entries[first:] {
		if entry.Head != nil || len(entry.Edits) > 0 {
			return false
		}
		for _, message := range entry.Model {
			t.take(message)
		}
		t.seen = float64(entry.Id)
	}
	return true
}

// assistant is the message as an assistant response, however the entry holds it.
func assistant(message ai.Message) (ai.AssistantMessage, bool) {
	switch typed := message.(type) {
	case ai.AssistantMessage:
		return typed, true
	case *ai.AssistantMessage:
		if typed != nil {
			return *typed, true
		}
	}
	return ai.AssistantMessage{}, false
}

// take is one message appended to the context, as pi-ai's estimate and Durable's context count it.
func (t *ContextTally) take(message ai.Message) {
	response, ok := assistant(message)
	if !ok {
		t.trailing += ai.EstimateMessageTokens(message)
		return
	}
	usage := response.Usage
	t.latest = &usage
	// Durable leaves these responses out of the model's context.
	switch response.StopReason {
	case ai.StopReasonAborted, ai.StopReasonError, ai.StopReasonDeferred:
		return
	}
	if reported := ai.CalculateContextTokens(response.Usage); reported > 0 {
		t.usage = reported
		t.trailing = 0
	} else {
		t.trailing += ai.EstimateMessageTokens(message)
	}
}

// ResponseCosts is each response of the active entries with its cost, in commit order.
func ResponseCosts(entries []durable.EntryRecord) []AssistantCost {
	costs := []AssistantCost{}
	for _, entry := range entries {
		if len(entry.Model) == 0 {
			continue
		}
		response, ok := assistant(entry.Model[0])
		if !ok {
			continue
		}
		costs = append(costs, AssistantCost{
			EntryID:    jsNumber(float64(entry.Id)),
			Timestamp:  response.Timestamp,
			Cost:       response.Usage.Cost.Total,
			CacheHit:   CacheHitRate(response.Usage),
			Provider:   response.Provider,
			Model:      response.Model,
			StopReason: string(response.StopReason),
		})
	}
	return costs
}
