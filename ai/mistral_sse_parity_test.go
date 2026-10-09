//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"io"
	"strings"
	"testing"
)

// Pi's Mistral SDK accepts an optional field space and joins repeated data fields (see TestProviderSSEFramingAndEOFRules).
func TestMistralSSEFramingAndEOFRules(t *testing.T) {
	t.Run("mistral", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
		provider := &mistralProvider{}
		provider.consumeStream(context.Background(), io.NopCloser(strings.NewReader(strings.Join([]string{
			`data:{"choices":[{"delta":{"content":`,
			`data:"hi"},"finish_reason":"stop"}]}`,
			"",
			"data:[DONE]",
		}, "\n"))), builder)
		assertSSETextResult(t, builder.stream.Result(), "hi")
	})
}
