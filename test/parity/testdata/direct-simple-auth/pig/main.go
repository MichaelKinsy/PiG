package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	output := map[string]string{}
	for _, api := range []ai.API{ai.APIAnthropicMessages, ai.APIAzureOpenAIResponses, ai.APIGoogleGenerativeAI, ai.APIMistralConversations, ai.APIOpenAICodexResponses, ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		model := &ai.Model{ID: "test-model", DisplayName: "Test", ProviderMeta: ai.ProviderMetadata{API: api, ProviderID: "test-provider", BaseURL: "https://example.invalid"}, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
		stream, err := ai.StreamSimple(context.Background(), model, ai.NormalizeContext(ai.Context{}), ai.StreamOptions{})
		if err == nil || stream != nil {
			return fmt.Errorf("%s did not reject before stream creation", api)
		}
		output[string(api)] = err.Error()
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}
