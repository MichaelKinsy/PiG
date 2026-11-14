//go:build !pig_strip_mistral_conversations

package ai

// connectionEndMistralShapes is the Mistral shape of connectionEndShapes, absent from a Piglet built with pig_strip_mistral_conversations.
// mistral-conversations.ts:172 reports error.message alone (formatMistralError), so the fetch rejection carries no cause.
func connectionEndMistralShapes() []connectionEndShape {
	return []connectionEndShape{{"mistral-conversations", MistralConversationsAPI, APIMistralConversations, "mistral", "mistral-large-latest", "test-key", "", func(string) string { return "fetch failed" }}}
}
