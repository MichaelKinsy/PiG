package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// BenchmarkProviderRequestShape measures conversion plus JSON encoding of a 64-turn transcript with mixed empty and non-empty text blocks.
func BenchmarkProviderRequestShape(b *testing.B) {
	var messages []Message
	for range 64 {
		messages = append(messages,
			UserMessage{Content: UserContentBlocks{TextContent{}, TextContent{Text: strings.Repeat("question ", 32)}}},
			AssistantMessage{Content: []AssistantContentBlock{TextContent{}, TextContent{Text: strings.Repeat("answer ", 128)}}},
		)
	}
	b.Run("responses", func(b *testing.B) {
		provider := &openAIResponsesProvider{}
		b.ReportAllocs()
		for b.Loop() {
			items, err := provider.convertMessages(messages, nil)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := json.Marshal(items); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("google", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			items := geminiConvertMessages(messages, "google", "test", true)
			if _, err := json.Marshal(items); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("completions-cache", func(b *testing.B) {
		cc := &oaiCacheControl{Type: "ephemeral"}
		b.ReportAllocs()
		for b.Loop() {
			items, err := convertMessages(messages, false, nil)
			if err != nil {
				b.Fatal(err)
			}
			applyAnthropicCacheControl(items, nil, cc)
			if _, err := json.Marshal(items); err != nil {
				b.Fatal(err)
			}
		}
	})
}
