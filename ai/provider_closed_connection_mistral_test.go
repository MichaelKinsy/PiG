//go:build !pig_strip_mistral_conversations

package ai

// closedConnectionMistralProviders is the Mistral case of closedConnectionProviders. Its SDK wording is the one the Google case notes: a
// fetch rejection reads "fetch failed", and the SDK relays no stream error event with the gateway's text, so the case has no gatewayEvent.
func closedConnectionMistralProviders() []closedConnectionProvider {
	return []closedConnectionProvider{{
		name: "mistral",
		make: func(u string) Provider {
			return NewMistralProvider(MistralConfig{BaseURL: u, APIKey: "test-key", Model: "mistral-small"})
		},
		beforeByte: "fetch failed", afterHeaders: "terminated", midStream: "terminated",
	}}
}
