//go:build pig_strip_mistral_conversations

package ai

// providerStreamEventRejectionMistralCases is empty: this build strips mistral-conversations.
func providerStreamEventRejectionMistralCases(_, _ string) []providerStreamEventRejectionCase {
	return nil
}
