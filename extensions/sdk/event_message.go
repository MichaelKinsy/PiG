package sdk

// Event payloads arrive as decoded JSON rather than typed structs, because the
// set of events is open and an extension subscribes by name. The message-shaped
// events all carry the same envelope:
//
//	{"type": "message_end", "message": {"role": "assistant", "content": [...]}}
//
// where content is a block array, or a string for a user message (Pi's UserMessage.content is `string | (TextContent | ImageContent)[]`). Reading it by hand is
// where extensions go wrong: content.(string) always fails, and guessing at
// Go-side field names like "Assistant" finds nothing, because the wire is
// upstream's flat role-discriminated union.

// MessageRole reports the role of an event's message payload, or "" when the
// event carries no message or its role is not a string.
func MessageRole(data map[string]any) string {
	msg, ok := data["message"].(map[string]any)
	if !ok {
		return ""
	}
	role, _ := msg["role"].(string)
	return role
}

// MessageText concatenates the text blocks of an event's message payload.
// Non-text blocks (tool calls, images, thinking) are skipped. Returns "" when
// the event carries no message or the message has no text.
func MessageText(data map[string]any) string {
	msg, ok := data["message"].(map[string]any)
	if !ok {
		return ""
	}
	return textFromContent(msg["content"])
}

// textFromContent reads a user message's string content as it is and joins the string text of the text blocks of a block array; any other content has no text.
func textFromContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var out []byte
		for _, raw := range v {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if kind, _ := block["type"].(string); kind != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				out = append(out, text...)
			}
		}
		return string(out)
	default:
		return ""
	}
}
