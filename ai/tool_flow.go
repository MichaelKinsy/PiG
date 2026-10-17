package ai

import (
	"slices"
	"time"
)

// Ports packages/ai/src/api/transform-messages.ts

// prepareProviderToolFlow applies transformMessages' tool-accounting pass before conversion. System updates wait behind pending results, failed assistant turns are omitted, and missing results close before a new assistant/user turn or the end of the transcript.
func prepareProviderToolFlow(transcript TranscriptContext) TranscriptContext {
	if transcript.err != nil {
		return transcript
	}
	result := make([]Message, 0, len(transcript.messages))
	var pending []ToolCall
	existing := map[string]bool{}
	var held []Message
	// pig divergence (D120): a tool call without a name, or still without an id after ID normalization, cannot be replayed to any provider, so it and the results that name its id are omitted (pi-mono #10299).
	var dropped map[string]bool
	closePending := func() {
		for _, call := range pending {
			if !existing[call.ID] {
				result = append(result, ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: "No result provided"}}, IsError: true, Timestamp: time.Now().UnixMilli()})
			}
		}
		if len(pending) > 0 {
			pending = pending[:0]
			clear(existing)
		}
		result = append(result, held...)
		held = nil
	}
	// Messages are appended as the interface values they arrived as: converting the type-switch copy back to Message would box every message again. Only an assistant message that lost unreplayable tool calls (D120) is the edited copy.
	for _, original := range transcript.messages {
		switch message := original.(type) {
		case AssistantMessage:
			closePending()
			if message.StopReason == StopReasonError || message.StopReason == StopReasonAborted {
				continue
			}
			kept := original
			if content, omitted := omitUnreplayableToolCalls(message.Content); omitted != nil {
				message.Content = content
				kept = message
				if dropped == nil {
					dropped = map[string]bool{}
				}
				for _, id := range omitted {
					dropped[id] = true
				}
			}
			for _, block := range message.Content {
				if call, ok := block.(ToolCall); ok {
					pending = append(pending, call)
				}
			}
			if len(pending) > 0 {
				clear(existing)
			}
			result = append(result, kept)
		case ToolResultMessage:
			if dropped[message.ToolCallID] {
				continue
			}
			existing[message.ToolCallID] = true
			result = append(result, original)
		case SystemMessage:
			if len(pending) > 0 {
				held = append(held, original)
			} else {
				result = append(result, original)
			}
		case UserMessage:
			closePending()
			result = append(result, original)
		default:
			result = append(result, original)
		}
	}
	closePending()
	return TranscriptContext{messages: result}
}

// omitUnreplayableToolCalls returns content without its tool calls that have a blank name or an empty id, and the ids of the omitted calls (an empty id included, so the results that name it are omitted too). It returns a nil id list, and content itself, when nothing is omitted; it never edits content in place.
func omitUnreplayableToolCalls(content []AssistantContentBlock) ([]AssistantContentBlock, []string) {
	unreplayable := func(block AssistantContentBlock) bool {
		call, ok := block.(ToolCall)
		return ok && (call.ID == "" || trimJSWhitespace(call.Name) == "")
	}
	if !slices.ContainsFunc(content, unreplayable) {
		return content, nil
	}
	kept := make([]AssistantContentBlock, 0, len(content))
	omitted := []string{}
	for _, block := range content {
		if !unreplayable(block) {
			kept = append(kept, block)
		} else {
			omitted = append(omitted, block.(ToolCall).ID)
		}
	}
	return kept, omitted
}
