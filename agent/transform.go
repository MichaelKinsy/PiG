package agent

// transform.go: cross-provider message normalization.
//
// Mirrors upstream:
//   .upstream/current/packages/ai/src/providers/transform-messages.ts
//
// transformMessages does several things that matter for multi-provider support:
//  1. Skip errored/aborted assistant messages so the model retries from the
//     last valid state rather than replaying a half-formed turn.
//  2. Insert synthetic "No result provided" tool results for any tool_use
//     block that has no matching tool_result (orphaned tool calls can occur
//     after context-cancelled or mid-stream aborts).
//  3. Normalize thinking blocks against the target model (the isSameModel
//     branch): drop cross-model redacted thinking, keep same-model signed
//     thinking, drop empty thinking, and convert other cross-model thinking to
//     plain text so a foreign reasoning signature never reaches a converter.
//  4. (future) Normalize tool call IDs for providers that require short
//     ^[a-zA-Z0-9_-]+ IDs (e.g. Anthropic). OpenAI accepts arbitrary IDs.
//  5. (future) Downgrade image blocks for non-vision models.
//
// pig routes several models through one provider (GitHub Copilot), so the
// thinking normalization is live whenever a session switches models (e.g.
// gpt-* to claude-*). This file is wired into convertToLLM in messages.go.
//
// NormalizeMessages is called before convertToLLM so filters work on the
// rich AgentMessage types (stopReason, full content), not the flattened
// ai.Message wire format.

import (
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// NormalizeMessages filters and normalizes msgs before they are converted to
// the provider wire format by convertToLLM. Two guarantees are provided:
//
//  1. Assistant messages with StopReason "error" or "aborted" are dropped.
//     Replaying them causes API errors (partial reasoning, orphaned tool
//     calls, OpenAI "reasoning without following item").
//
//  2. Orphaned tool calls: tool_use blocks in an assistant message with no
//     matching tool_result: get a synthetic ToolResultMessage injected
//     immediately after the assistant message. This keeps the message sequence
//     valid for all providers.
//
//  3. Cross-model thinking blocks are normalized against the target model
//     (upstream transform-messages.ts isSameModel branch): redacted thinking is
//     dropped for a different model, same-model signed thinking is kept for
//     replay, empty thinking is dropped, and other cross-model thinking is
//     converted to plain text so a foreign reasoning signature never reaches a
//     provider converter. model may be nil on contextless/test paths, which is
//     treated as same-model (thinking passes through).
//
// The returned slice may be shorter than msgs (dropped aborted/errored entries)
// or longer (inserted synthetic tool results). Assistant entries whose thinking
// is normalized are copied; other non-modified entries share their underlying
// struct pointers with msgs.
func NormalizeMessages(msgs []AgentMessage, model *ai.Model) []AgentMessage {
	if len(msgs) == 0 {
		return msgs
	}

	result := make([]AgentMessage, 0, len(msgs))
	var pendingToolUseIDs []string
	var pendingToolNames []string
	seenResultIDs := map[string]bool{}

	// A result that arrives after the next assistant turn is not missing, only
	// out of order. Upstream fills such a call with an error placeholder and
	// still sends the late copy, which the provider rejects. Reporting a tool as
	// failed when it succeeded is the worse half of that: the model may retry an
	// operation that already ran. Recovering the real content lets the
	// placeholder slot carry the truth, and the duplicate pass below drops the
	// copy left behind.
	//
	// Queued per id and consumed in order, because a call id is not unique
	// across a conversation: a provider may number its calls per response, so
	// the same id reappears each turn. Taking the first match globally would
	// answer a later turn's call with an earlier turn's output.
	unconsumedResults := map[string][]*ToolResultMessage{}
	for i := range msgs {
		if tr := msgs[i].ToolResult; tr != nil && tr.ToolCallID != "" {
			unconsumedResults[tr.ToolCallID] = append(unconsumedResults[tr.ToolCallID], tr)
		}
	}
	consumeResult := func(id string) *ToolResultMessage {
		queued := unconsumedResults[id]
		if len(queued) == 0 {
			return nil
		}
		unconsumedResults[id] = queued[1:]
		return queued[0]
	}

	flushSynthetic := func() {
		for i, id := range pendingToolUseIDs {
			if seenResultIDs[id] {
				continue
			}
			name := ""
			if i < len(pendingToolNames) {
				name = pendingToolNames[i]
			}
			if real := consumeResult(id); real != nil {
				moved := *real
				moved.Role = RoleToolResult
				result = append(result, AgentMessage{ToolResult: &moved})
				continue
			}
			result = append(result, AgentMessage{
				ToolResult: &ToolResultMessage{
					Role:       RoleToolResult,
					ToolCallID: id,
					ToolName:   name,
					Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: "No result provided"}},
					IsError:    true,
					Timestamp:  time.Now().UnixMilli(),
				},
			})
		}
		pendingToolUseIDs = pendingToolUseIDs[:0]
		pendingToolNames = pendingToolNames[:0]
		seenResultIDs = map[string]bool{}
	}

	for _, msg := range msgs {
		switch {
		case msg.Assistant != nil:
			// Flush orphaned tool calls from the PREVIOUS assistant message
			// before we start tracking this one.
			flushSynthetic()

			// Drop errored/aborted assistant messages: they represent
			// incomplete turns that the model should not replay.
			// (upstream transform-messages.ts:153-159)
			if r := msg.Assistant.StopReason; r == "error" || r == "aborted" {
				continue
			}

			// Normalize thinking blocks against the target model before the
			// provider converter sees them (upstream transform-messages.ts
			// isSameModel branch). Copy the assistant message only when a block
			// actually changed, so unmodified turns still share their pointer.
			if norm, changed := normalizeAssistantReplayBlocks(msg.Assistant.Content, sameModel(msg.Assistant, model)); changed {
				cp := *msg.Assistant
				cp.Content = norm
				msg.Assistant = &cp
			}

			// Collect tool_use IDs for orphan detection.
			pendingToolUseIDs = pendingToolUseIDs[:0]
			pendingToolNames = pendingToolNames[:0]
			seenResultIDs = map[string]bool{}
			for _, blk := range msg.Assistant.Content {
				if call, ok := blk.(ai.ToolCall); ok && call.ID != "" {
					pendingToolUseIDs = append(pendingToolUseIDs, call.ID)
					pendingToolNames = append(pendingToolNames, call.Name)
				}
			}
			result = append(result, msg)

		case msg.ToolResult != nil:
			// Record fulfilled tool IDs.
			seenResultIDs[msg.ToolResult.ToolCallID] = true
			consumeResult(msg.ToolResult.ToolCallID)
			result = append(result, msg)

		case msg.User != nil:
			// User message interrupts any pending tool flow: flush first.
			flushSynthetic()
			result = append(result, msg)

		default:
			result = append(result, msg)
		}
	}

	// Flush trailing orphaned tool calls.
	flushSynthetic()

	// Second pass: strip orphaned tool results (results whose tool_call_id
	// doesn't match any tool_use in the conversation). This happens when the
	// assistant turn that made the call was dropped above (errored/aborted) or
	// never existed (truncated/hand-built session, model switch) while the
	// tool_result survived.
	//
	// pig divergence (D48): drop results without an open tool call. Upstream
	// sends them, and providers reject the request.
	//
	// Matched in order against open calls rather than against the set of ids in
	// the conversation. A call id is not unique across a conversation when a
	// provider numbers its calls per response, so the same id opens a new call
	// each turn, and a result closes the call that is open when it arrives.
	// Counting alone is not enough either: it keeps the first N results for an
	// id, which lets a duplicate of an early call consume the allowance that
	// belonged to a later one.
	anyToolUse := false
	for _, msg := range result {
		if msg.Assistant == nil {
			continue
		}
		for _, blk := range msg.Assistant.Content {
			if call, ok := blk.(ai.ToolCall); ok && call.ID != "" {
				anyToolUse = true
				break
			}
		}
		if anyToolUse {
			break
		}
	}
	// Drop repeated results. The first result closes the open call.
	if anyToolUse || hasToolResults(result) {
		filtered := make([]AgentMessage, 0, len(result))
		openCalls := map[string]int{}
		for _, msg := range result {
			if msg.Assistant != nil {
				for _, blk := range msg.Assistant.Content {
					if call, ok := blk.(ai.ToolCall); ok && call.ID != "" {
						openCalls[call.ID]++
					}
				}
			}
			if msg.ToolResult != nil {
				id := msg.ToolResult.ToolCallID
				if openCalls[id] == 0 {
					continue
				}
				openCalls[id]--
			}
			filtered = append(filtered, msg)
		}
		result = filtered
	}

	return result
}

// sameModel reports whether an assistant message was produced by the target
// model. Mirrors upstream transform-messages.ts isSameModel using provider,
// API, and model ID. A nil target model (contextless or test path) is treated
// as same-model so signed content passes through unchanged.
func sameModel(a *AssistantMessage, model *ai.Model) bool {
	if model == nil || model.Provider == nil {
		return true
	}
	return a.Provider == model.Provider.ID() && a.API == model.ProviderMeta.API && a.ModelID == model.ID
}

// normalizeAssistantReplayBlocks rewrites assistant content for cross-model
// replay, mirroring upstream transform-messages.ts. It returns the (possibly
// rewritten) content and whether anything changed. Same-model signed thinking is
// kept even when its text is empty (encrypted reasoning); the provider converter
// drops the empty-text block if the API rejects it. A tool-call thoughtSignature
// from a different provider/model is unusable by the target provider, so it is
// stripped here (transform-messages.ts:131) before the converter can leak it
// (e.g. an OpenAI reasoning.encrypted blob into a Google request).
func normalizeAssistantReplayBlocks(blocks []ai.AssistantContentBlock, isSameModel bool) ([]ai.AssistantContentBlock, bool) {
	changed := false
	out := make([]ai.AssistantContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := block.(ai.TextContent); ok {
			if !isSameModel && text.TextSignature != "" {
				text.TextSignature = ""
				out = append(out, text)
				changed = true
			} else {
				out = append(out, block)
			}
			continue
		}
		if call, ok := block.(ai.ToolCall); ok {
			if !isSameModel && call.ThoughtSignature != "" {
				call.ThoughtSignature = ""
				out = append(out, call)
				changed = true
			} else {
				out = append(out, block)
			}
			continue
		}
		thinking, ok := block.(ai.ThinkingContent)
		if !ok {
			out = append(out, block)
			continue
		}
		switch {
		case thinking.Redacted:
			if isSameModel {
				out = append(out, block)
			} else {
				changed = true
			}
		case isSameModel && thinking.ThinkingSignature != "":
			out = append(out, block)
		case strings.TrimSpace(thinking.Thinking) == "":
			changed = true
		case isSameModel:
			out = append(out, block)
		default:
			out = append(out, ai.TextContent{Text: thinking.Thinking})
			changed = true
		}
	}
	return out, changed
}

// hasToolResults returns true if any message in msgs is a tool result.
func hasToolResults(msgs []AgentMessage) bool {
	for _, m := range msgs {
		if m.ToolResult != nil {
			return true
		}
	}
	return false
}
