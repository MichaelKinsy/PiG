//go:build !pig_strip_mistral_conversations

package ai

import (
	"bytes"
	"testing"
)

// withTopLevelEvent puts `data: <value>` after the first record of each of the first count bodies, as one chunk.
func withTopLevelEvent(bodies [][][]byte, value string, count int) [][][]byte {
	var out [][][]byte
	for _, body := range bodies[:count] {
		joined := bytes.Join(body, nil)
		end := bytes.Index(joined, []byte("\n\n"))
		if end < 0 {
			continue
		}
		var injected []byte
		injected = append(injected, joined[:end+2]...)
		injected = append(injected, "data: "+value+"\n\n"...)
		injected = append(injected, joined[end+2:]...)
		out = append(out, [][]byte{injected})
	}
	return out
}

// TestProviderStreamNonObjectEventsMatchPi sends an event whose JSON is not an object (a number, string, boolean, array or null) in the middle of each
// provider's stream. Each SDK and provider treats it its own way: the OpenAI SDK yields it and processResponsesStream reads `event.type` (null throws),
// @google/genai maps it to an empty response, the Anthropic, completions and Mistral readers skip or reject it, and Pig must do the same.
func TestProviderStreamNonObjectEventsMatchPi(t *testing.T) {
	t.Setenv("PIG_ORACLE_KEY_CASE", "float")
	for _, value := range []string{"5", "null", "[1]", `"x"`, "false"} {
		t.Run("anthropic "+value, func(t *testing.T) {
			providerStreamOracle(t, [4]string{"anthropic-messages", "anthropicMessagesApi", "anthropic-messages", "anthropic"}, true, withTopLevelEvent(anthropicStreamBodies(), value, 20), func(baseURL string) Provider {
				return NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
			})
		})
		t.Run("openai-completions "+value, func(t *testing.T) {
			providerStreamOracle(t, [4]string{"openai-completions", "openAICompletionsApi", "openai-completions", "openai"}, true, withTopLevelEvent(openAICompletionsStreamBodies(), value, 20), func(baseURL string) Provider {
				return NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
			})
		})
		t.Run("openai-responses "+value, func(t *testing.T) {
			providerStreamOracle(t, [4]string{"openai-responses", "openAIResponsesApi", "openai-responses", "openai"}, true, withTopLevelEvent(openAIResponsesStreamBodies(), value, 20), func(baseURL string) Provider {
				return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
			})
		})
		t.Run("azure-openai-responses "+value, func(t *testing.T) {
			providerStreamOracle(t, [4]string{"azure-openai-responses", "azureOpenAIResponsesApi", "azure-openai-responses", "azure-openai-responses"}, true, withTopLevelEvent(openAIResponsesStreamBodies(), value, 20), func(baseURL string) Provider {
				return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
			})
		})
		t.Run("google "+value, func(t *testing.T) {
			providerStreamOracle(t, [4]string{"google-generative-ai", "googleGenerativeAIApi", "google-generative-ai", "google"}, false, withTopLevelEvent(googleStreamBodies(), value, 20), func(baseURL string) Provider {
				return NewGoogleProvider(GoogleConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
			})
		})
		t.Run("mistral "+value, func(t *testing.T) {
			providerStreamOracle(t, [4]string{"mistral-conversations", "mistralConversationsApi", "mistral-conversations", "mistral"}, true, withTopLevelEvent(mistralStreamBodies(), value, 20), func(baseURL string) Provider {
				return NewMistralProvider(MistralConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
			})
		})
	}
}
