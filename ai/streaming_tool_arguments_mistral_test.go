//go:build !pig_strip_mistral_conversations

package ai

// The Mistral path of the streamed tool-call argument tests; a build that strips the Mistral provider leaves it out.
func init() {
	streamingArgumentsPaths = append(streamingArgumentsPaths, streamingArgumentsPath{
		name: "mistral-conversations",
		provider: func(url string) Provider {
			return NewMistralProvider(MistralConfig{BaseURL: url, APIKey: "test", Model: "devstral", ProviderID: "mistral"})
		},
		body: func(deltas []string, _ JsonObject, _ string) string {
			return chatStreamingBody(deltas, true)
		},
	})
}
