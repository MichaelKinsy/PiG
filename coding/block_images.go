package coding

import "github.com/MichaelKinsy/PiG/ai"

// blockedImageText replaces each image when images.blockImages is on
// (sdk.ts convertToLlmWithBlockImages).
const blockedImageText = "Image reading is disabled."

// blockImages mirrors upstream convertToLlmWithBlockImages: in user and
// toolResult messages that contain an image, each image becomes a
// blockedImageText text block, and a blockedImageText text that directly
// follows another is dropped. Other messages pass through unchanged.
func blockImages(messages []ai.Message) []ai.Message {
	out := make([]ai.Message, len(messages))
	for i, message := range messages {
		switch message := message.(type) {
		case ai.UserMessage:
			if blocks, ok := message.Content.(ai.UserContentBlocks); ok && hasImage(blocks) {
				message.Content = replaceImages(blocks, func(text string) ai.UserContentBlock { return ai.TextContent{Text: text} })
			}
			out[i] = message
		case ai.ToolResultMessage:
			if hasImage(message.Content) {
				message.Content = replaceImages(message.Content, func(text string) ai.ToolResultMessageContent { return ai.TextContent{Text: text} })
			}
			out[i] = message
		default:
			out[i] = message
		}
	}
	return out
}

func hasImage[B any](blocks []B) bool {
	for _, block := range blocks {
		if _, ok := any(block).(ai.ImageContent); ok {
			return true
		}
	}
	return false
}

func replaceImages[S ~[]B, B any](blocks S, text func(string) B) S {
	mapped := make([]B, len(blocks))
	for i, block := range blocks {
		if _, ok := any(block).(ai.ImageContent); ok {
			mapped[i] = text(blockedImageText)
		} else {
			mapped[i] = block
		}
	}
	isPlaceholder := func(block B) bool {
		text, ok := any(block).(ai.TextContent)
		return ok && text.Text == blockedImageText
	}
	out := make(S, 0, len(mapped))
	for i, block := range mapped {
		if i > 0 && isPlaceholder(block) && isPlaceholder(mapped[i-1]) {
			continue
		}
		out = append(out, block)
	}
	return out
}
