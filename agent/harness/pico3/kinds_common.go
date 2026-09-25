package pico3

import (
	"context"
	"encoding/json"
	"math"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Kinds are the built-in kinds, as typed witnesses for createTask, waiting,
// and hook registration.
var Kinds = BuiltinKinds{
	Generation: generationKind,
	Tool:       toolKind,
	PostTools:  postToolsKind,
	Collapse:   collapseKind,
	Job:        jobKind,
	Plugin:     pluginKind,
}

// BuiltinEntries are the built-in entry kinds.
type BuiltinEntries struct {
	User       *EntryKind
	Assistant  *EntryKind
	ToolResult *EntryKind
	System     *EntryKind
	Notice     *EntryKind
	Usage      *EntryKind
	Summary    *EntryKind
	Handoff    *EntryKind
	Reset      *EntryKind
}

func (entries BuiltinEntries) list() []*EntryKind {
	return []*EntryKind{entries.User, entries.Assistant, entries.ToolResult, entries.System, entries.Notice, entries.Usage, entries.Summary, entries.Handoff, entries.Reset}
}

// Entries are the built-in entry kind witnesses.
var Entries = BuiltinEntries{
	User:       &EntryKind{Kind: "pi.user"},
	Assistant:  &EntryKind{Kind: "pi.assistant"},
	ToolResult: &EntryKind{Kind: "pi.tool_result"},
	System:     &EntryKind{Kind: "pi.system"},
	Notice:     &EntryKind{Kind: "pi.notice"},
	Usage:      &EntryKind{Kind: "pi.usage"},
	Summary:    &EntryKind{Kind: "pi.summary"},
	Handoff:    &EntryKind{Kind: "pi.handoff"},
	Reset:      &EntryKind{Kind: "pi.reset"},
}

// coreSticky returns a conversation's live sticky document (core only).
func coreSticky(tx *Tx, conversationId Id) (JsonObject, error) {
	if err := tx.assertCore("sticky document"); err != nil {
		return nil, err
	}
	return tx.raw(StickyDoc(conversationId))
}

// coreRewindable returns a conversation's live rewindable document (core
// only).
func coreRewindable(tx *Tx, conversationId Id) (JsonObject, error) {
	if err := tx.assertCore("rewindable document"); err != nil {
		return nil, err
	}
	return tx.raw(RewindableDoc(conversationId))
}

// resetTurn clears the turn view.
func resetTurn(tx *Tx, conversationId Id) error {
	sticky, err := coreSticky(tx, conversationId)
	if err != nil {
		return err
	}
	sticky["turn"] = JsonObject{"tools": []any{}}
	return nil
}

// turnOf returns the sticky turn object.
func turnOf(tx *Tx, conversationId Id) (JsonObject, error) {
	sticky, err := coreSticky(tx, conversationId)
	if err != nil {
		return nil, err
	}
	turn, ok := sticky["turn"].(map[string]any)
	if !ok {
		turn = JsonObject{"tools": []any{}}
		sticky["turn"] = turn
	}
	return turn, nil
}

func emit(tx *Tx, event ViewEvent) error { return tx.EmitEvent(event) }

func userMessage(content JsonValue, now float64) JsonObject {
	return JsonObject{"role": "user", "content": content, "timestamp": now}
}

// storedMessage converts an assistant message to its stored JSON form.
func storedMessage(message ai.AssistantMessage) JsonObject {
	if message.Content == nil {
		message.Content = []ai.AssistantContentBlock{}
	}
	stored := storedObject(message)
	stored["role"] = "assistant"
	return stored
}

// estimateTokens estimates what a request sends: pi-ai drops system messages
// and aborted or failed assistant messages before sending.
func estimateTokens(messages []JsonObject, dropFailedAssistants bool) int {
	var agentMessages []agent.AgentMessage
	for _, message := range messages {
		role := str(message, "role")
		if role == "system" {
			continue
		}
		stop := str(message, "stopReason")
		if dropFailedAssistants && role == "assistant" && (stop == "aborted" || stop == "error") {
			continue
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			continue
		}
		var decoded agent.AgentMessage
		if json.Unmarshal(encoded, &decoded) == nil {
			agentMessages = append(agentMessages, decoded)
		}
	}
	return agent.EstimateContextTokens(agentMessages).Tokens
}

type retryDecisionResult struct {
	retry   bool
	untilMs float64
	reason  string
}

// retryDecision decides a failed attempt's fate under the captured policy.
func retryDecision(policy RetryPolicy, attempt int, message *ai.AssistantMessage, now float64) retryDecisionResult {
	if message != nil && !ai.IsRetryableAssistantError(*message) {
		return retryDecisionResult{reason: "provider"}
	}
	if !policy.Enabled {
		return retryDecisionResult{reason: "provider"}
	}
	if float64(attempt) > policy.MaxRetries {
		return retryDecisionResult{reason: "retries_exhausted"}
	}
	delay := policy.BaseDelayMs * math.Pow(2, math.Max(0, float64(attempt-1)))
	if delay > 1<<53-1 || delay != math.Trunc(delay) {
		delay = 1<<53 - 1
	}
	limit := 60_000.0 // upstream: agent/src/harness/pico3/kinds/generation.ts:maxAgentDelayMs
	if policy.MaxAgentDelayMs != nil {
		limit = *policy.MaxAgentDelayMs
	}
	return retryDecisionResult{retry: true, untilMs: now + math.Min(delay, limit)}
}

func done(completion Completion) Step {
	return Step{Done: func(context.Context, *Tx, Task) (Completion, error) { return completion, nil }}
}
