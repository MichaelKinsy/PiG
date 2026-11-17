//go:build !pig_strip_mistral_conversations

package coding

import "github.com/MichaelKinsy/PiG/ai"

// The Mistral row of the request body test; a build that strips the Mistral provider leaves it out. Mistral sends tools strict only when the model's
// compat sets supportsStrictMode (Pi 2c2a77f9f3ab), so the row's model sets it: the row keeps proving that the strict conversion of a held schema writes
// the bytes the golden holds.
func init() {
	strippableRequestShapes = append(strippableRequestShapes, requestShape{name: "mistral", reply: chatReply, path: "", provider: func(base string) ai.Provider {
		strictMode := true
		return ai.NewMistralProvider(ai.MistralConfig{BaseURL: base, APIKey: "k", Model: "model-a", ModelMetadata: &ai.Model{
			ID: "model-a", Input: []string{"text"},
			ProviderMeta: ai.ProviderMetadata{API: ai.APIMistralConversations, BaseURL: base, Compat: &ai.ModelCompat{SupportsStrictMode: &strictMode}},
			Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh},
		}})
	}})
}
