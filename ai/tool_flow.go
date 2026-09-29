package ai

import "time"

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
	closePending := func() {
		for _, call := range pending {
			if !existing[call.ID] {
				result = append(result, ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: "No result provided"}}, IsError: true, Timestamp: time.Now().UnixMilli()})
			}
		}
		if len(pending) > 0 {
			pending = nil
			existing = map[string]bool{}
		}
		result = append(result, held...)
		held = nil
	}
	for _, message := range transcript.messages {
		switch message := message.(type) {
		case AssistantMessage:
			closePending()
			if message.StopReason == StopReasonError || message.StopReason == StopReasonAborted {
				continue
			}
			for _, block := range message.Content {
				if call, ok := block.(ToolCall); ok {
					pending = append(pending, call)
				}
			}
			if len(pending) > 0 {
				existing = map[string]bool{}
			}
			result = append(result, message)
		case ToolResultMessage:
			existing[message.ToolCallID] = true
			result = append(result, message)
		case SystemMessage:
			if len(pending) > 0 {
				held = append(held, message)
			} else {
				result = append(result, message)
			}
		case UserMessage:
			closePending()
			result = append(result, message)
		default:
			result = append(result, message)
		}
	}
	closePending()
	return TranscriptContext{messages: result}
}
