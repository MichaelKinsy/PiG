//go:build !pig_strip_mistral_conversations

package ai

// providerErrorMistralCases is the Mistral case of TestProviderErrorResponsesMatchPi.
func providerErrorMistralCases() []providerErrorCase {
	return []providerErrorCase{{"mistral", [4]string{"mistral-conversations", "mistralConversationsApi", "mistral-conversations", "mistral"}, func(baseURL string) Provider {
		return NewMistralProvider(MistralConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	}}}
}

// providerDroppedMistralCases is the Mistral case of TestProviderDroppedConnectionMatchesPi.
func providerDroppedMistralCases() []providerDroppedCase {
	return []providerDroppedCase{{"mistral", [4]string{"mistral-conversations", "mistralConversationsApi", "mistral-conversations", "mistral"}, true, mistralStreamBodies, func(baseURL string) Provider {
		return NewMistralProvider(MistralConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	}}}
}
