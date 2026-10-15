package ai

// Ports packages/ai/src/api/transform-messages.ts.

import (
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// trimJSWhitespace matches String.prototype.trim: ECMAScript includes BOM but excludes NEL.
func trimJSWhitespace(value string) string { return jsstring.Trim(value) }

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

// sameModelBlocksUnchanged reports whether the transformation below keeps every block of a same-model assistant message as it is: text, tool calls, and thinking that is redacted, signed or non-blank. The message then keeps the interface value it arrived as.
func sameModelBlocksUnchanged(content []AssistantContentBlock) bool {
	if content == nil {
		return false
	}
	for _, value := range content {
		switch block := value.(type) {
		case TextContent, ToolCall:
		case ThinkingContent:
			if !block.Redacted && block.ThinkingSignature == "" && trimJSWhitespace(block.Thinking) == "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// TransformMessages prepares history for a target provider without changing retained messages. It preserves valid same-model signatures, normalizes foreign content and paired IDs, drops incomplete assistant turns, and closes unanswered tool calls before the next turn while holding intervening system updates.
func TransformMessages(messages []Message, model *Model, normalizeToolCallId func(string, *Model, AssistantMessage) string) []Message {
	return transformMessages(messages, model, normalizeToolCallId, false)
}

// transformMessages is TransformMessages. With keepForeignThinking another model's plain-text thinking stays a thinking block
// without its foreign signature, for a target that replays reasoning in its own field.
func transformMessages(messages []Message, model *Model, normalizeToolCallId func(string, *Model, AssistantMessage) string, keepForeignThinking bool) []Message {
	transformed := make([]Message, 0, len(messages))
	toolCallIdMap := make(map[string]string)
	supportsImages := slices.Contains(model.Input, "image")
	for _, value := range messages {
		switch message := value.(type) {
		case SystemMessage:
			if message.Content != nil {
				transformed = append(transformed, value)
				continue
			}
			message.Content = SystemTextBlocks{}
			transformed = append(transformed, message)
		case UserMessage:
			// A message that needs no change keeps the interface value it arrived as; boxing the copy would allocate it again.
			if _, blocks := message.Content.(UserContentBlocks); message.Content != nil && (supportsImages || !blocks) {
				transformed = append(transformed, value)
				continue
			}
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
			if id := toolCallIdMap[message.ToolCallID]; message.Content != nil && supportsImages && (id == "" || id == message.ToolCallID) {
				transformed = append(transformed, value)
				continue
			}
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
			if sameModel && sameModelBlocksUnchanged(message.Content) {
				transformed = append(transformed, value)
				continue
			}
			blocks := make([]AssistantContentBlock, 0, len(message.Content))
			// A thinking-only turn keeps Pi's text replay: its reasoning_content alone would not keep the message in the request.
			keepThinking := keepForeignThinking && slices.ContainsFunc(message.Content, func(block AssistantContentBlock) bool {
				switch block := block.(type) {
				case TextContent:
					return trimJSWhitespace(block.Text) != ""
				case ToolCall:
					return true
				}
				return false
			})
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
					case keepThinking:
						// pig divergence (D126): Pi inlines this as assistant text, so a target that requires reasoning_content got "" (pi-mono #6167).
						block.ThinkingSignature = ""
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
