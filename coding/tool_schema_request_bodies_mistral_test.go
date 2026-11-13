//go:build !pig_strip_mistral_conversations

package coding

import "github.com/MichaelKinsy/PiG/ai"

// The Mistral row of the request body test; a build that strips the Mistral provider leaves it out.
func init() {
	strippableRequestShapes = append(strippableRequestShapes, requestShape{name: "mistral", reply: chatReply, path: "", provider: func(base string) ai.Provider {
		return ai.NewMistralProvider(ai.MistralConfig{BaseURL: base, APIKey: "k", Model: "model-a"})
	}})
}
