//go:build pig_strip_mistral_conversations

package ai

// mistralToolWireKinds is empty: this build strips mistral-conversations.
func mistralToolWireKinds([]ToolSchema) []toolWireKind { return nil }
