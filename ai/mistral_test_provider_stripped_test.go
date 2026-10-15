//go:build pig_strip_mistral_conversations

package ai

import "net/http"

// newMistralTestProvider panics: this build strips mistral-conversations.
func newMistralTestProvider(cfg MistralConfig, _ *http.Client) Provider {
	panic(strippedMistralError(&cfg))
}
