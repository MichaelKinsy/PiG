//go:build pig_strip_mistral_conversations

package ai

// A Piglet built with pig_strip_mistral_conversations has no Mistral API module.
func connectionEndMistralShapes() []connectionEndShape { return nil }
