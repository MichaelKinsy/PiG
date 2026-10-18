package ai

// Ports packages/ai/src/api/transform-messages.ts.

import (
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// trimJSWhitespace matches String.prototype.trim: ECMAScript includes BOM but excludes NEL.
func trimJSWhitespace(value string) string { return jsstring.Trim(value) }

const errorToolImagePlaceholder = "(image attached after the tool results)"

// SplitErrorToolResult is an error tool result split into a text-only result and the images to send after the tool
// results (transform-messages.ts SplitErrorToolResult).
type SplitErrorToolResult struct {
	// Content is the result content with each run of images replaced by a note.
	Content []TextContent
	// Caption is the text naming the tool call, to send before Images.
	Caption string
	Images  []ImageContent
}

// SplitErrorToolResultImages splits the images out of an error tool result, for APIs that reject images in error
// results (Anthropic, and Claude on Bedrock). Send Content as the tool result and the caption and images after all tool
// results of the turn, in the same user message. It returns nil for a result that needs no split (Pi #10794,
// transform-messages.ts splitErrorToolResultImages).
func SplitErrorToolResultImages(message ToolResultMessage) *SplitErrorToolResult {
	if !message.IsError {
		return nil
	}
	var images []ImageContent
	for _, block := range message.Content {
		if image, ok := block.(ImageContent); ok {
			images = append(images, image)
		}
	}
	if len(images) == 0 {
		return nil
	}
	return &SplitErrorToolResult{
		Content: replaceImagesWithPlaceholder(message.Content, errorToolImagePlaceholder),
		Caption: "Images from the error result of tool call " + message.ToolCallID + " (" + message.ToolName + "):",
		Images:  images,
	}
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

// textBlocksOnly reports whether replaceImagesWithPlaceholder would return content's blocks unchanged: the slice exists and holds only text. A nil slice still becomes an empty one.
func textBlocksOnly[T ContentBlock](content []T) bool {
	if content == nil {
		return false
	}
	for _, value := range content {
		if _, ok := any(value).(TextContent); !ok {
			return false
		}
	}
	return true
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

// TransformMessages prepares history for a target provider without changing retained messages. It preserves valid same-model signatures, drops another model's thinking, normalizes foreign content and paired IDs, drops incomplete assistant turns, and closes unanswered tool calls before the next turn while holding intervening system updates.
func TransformMessages(messages []Message, model *Model, normalizeToolCallId func(string, *Model, AssistantMessage) string) []Message {
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
			if blocks, isBlocks := message.Content.(UserContentBlocks); message.Content != nil && (supportsImages || !isBlocks || textBlocksOnly(blocks)) {
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
			if id := toolCallIdMap[message.ToolCallID]; (supportsImages && message.Content != nil || textBlocksOnly(message.Content)) && (id == "" || id == message.ToolCallID) {
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
			for _, value := range message.Content {
				switch block := value.(type) {
				case ThinkingContent:
					// Another model's thinking is dropped: signatures and encrypted reasoning only work for the model that produced them, and
					// readable thinking sent as text makes the new model treat it as part of its reply (transform-messages.ts:129-137, Pi #11000).
					// Redacted and signed thinking of the same model stays even when its text is empty (encrypted reasoning).
					switch {
					case !sameModel:
					case block.Redacted || block.ThinkingSignature != "":
						blocks = append(blocks, block)
					case trimJSWhitespace(block.Thinking) == "":
					default:
						blocks = append(blocks, block)
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
