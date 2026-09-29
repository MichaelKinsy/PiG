package ai

// Ports packages/ai/src/api/transform-messages.ts.

import (
	"slices"
	"strings"
)

// trimJSWhitespace matches String.prototype.trim: ECMAScript includes BOM but excludes NEL.
func trimJSWhitespace(value string) string {
	return strings.Trim(value, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
}

func replaceImagesWithPlaceholder[T ContentBlock](content []T, placeholder string) []TextContent {
	result := make([]TextContent, 0, len(content))
	previousWasPlaceholder := false
	for _, value := range content {
		switch block := any(value).(type) {
		case ImageContent:
			if !previousWasPlaceholder {
				result = append(result, TextContent{Text: placeholder})
			}
			previousWasPlaceholder = true
		case TextContent:
			result = append(result, block)
			previousWasPlaceholder = block.Text == placeholder
		}
	}
	return result
}

// TransformMessages prepares history for a target provider without changing retained messages. It preserves valid same-model signatures, normalizes foreign content and paired IDs, drops incomplete assistant turns, and closes unanswered tool calls before the next turn while holding intervening system updates.
func TransformMessages(messages []Message, model *Model, normalizeToolCallId func(string, *Model, AssistantMessage) string) []Message {
	transformed := make([]Message, 0, len(messages))
	toolCallIdMap := make(map[string]string)
	supportsImages := slices.Contains(model.Input, "image")
	for _, value := range messages {
		switch message := value.(type) {
		case SystemMessage:
			if message.Content == nil {
				message.Content = SystemTextBlocks{}
			}
			transformed = append(transformed, message)
		case UserMessage:
			if message.Content == nil {
				message.Content = UserContentBlocks{}
			}
			if content, ok := message.Content.(UserContentBlocks); ok && !supportsImages {
				blocks := replaceImagesWithPlaceholder(content, "(image omitted: model does not support images)")
				result := make(UserContentBlocks, len(blocks))
				for i, block := range blocks {
					result[i] = block
				}
				message.Content = result
			}
			transformed = append(transformed, message)
		case ToolResultMessage:
			if message.Content == nil {
				message.Content = []ToolResultMessageContent{}
			}
			if !supportsImages {
				blocks := replaceImagesWithPlaceholder(message.Content, "(tool image omitted: model does not support images)")
				result := make([]ToolResultMessageContent, len(blocks))
				for i, block := range blocks {
					result[i] = block
				}
				message.Content = result
			}
			if id := toolCallIdMap[message.ToolCallID]; id != "" && id != message.ToolCallID {
				message.ToolCallID = id
			}
			transformed = append(transformed, message)
		case AssistantMessage:
			sameModel := message.Provider == model.ProviderMeta.ProviderID && message.API == model.ProviderMeta.API && message.Model == model.ID
			blocks := make([]AssistantContentBlock, 0, len(message.Content))
			for _, value := range message.Content {
				switch block := value.(type) {
				case ThinkingContent:
					switch {
					case block.Redacted:
						if sameModel {
							blocks = append(blocks, block)
						}
					case sameModel && block.ThinkingSignature != "":
						blocks = append(blocks, block)
					case trimJSWhitespace(block.Thinking) == "":
					case sameModel:
						blocks = append(blocks, block)
					default:
						blocks = append(blocks, TextContent{Text: block.Thinking})
					}
				case TextContent:
					if !sameModel {
						block.TextSignature = ""
					}
					blocks = append(blocks, block)
				case ToolCall:
					if !sameModel {
						block.ThoughtSignature = ""
						if normalizeToolCallId != nil {
							id := normalizeToolCallId(block.ID, model, message)
							if id != block.ID {
								toolCallIdMap[block.ID] = id
								block.ID = id
							}
						}
					}
					blocks = append(blocks, block)
				}
			}
			message.Content = blocks
			transformed = append(transformed, message)
		}
	}
	return prepareProviderToolFlow(TranscriptContext{messages: transformed}).messages
}
