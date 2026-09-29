package ai

import "testing"

// Pi openai-completions.ts:563,572 assigns usage only when that chunk supplies usage. Its finalization does not replace output.usage.
func TestCompletionsUsageReferenceChangesOnlyOnUsageChunks(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "test", "model")
	current := func() *Usage {
		builder.cell.mu.RLock()
		defer builder.cell.mu.RUnlock()
		return builder.cell.nested.usage
	}
	var first, last *Usage
	reader := &providerScratchReader{
		frames: []string{
			"data: {\"usage\":{\"prompt_tokens\":1},\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n",
			"data: {\"usage\":{\"prompt_tokens\":3},\"choices\":[{\"delta\":{\"content\":\"c\"},\"finish_reason\":\"stop\"}]}\n\n",
			"data: [DONE]\n\n",
		},
		beforeRead: func(index int) {
			switch index {
			case 1:
				first = current()
			case 2:
				if current() != first {
					t.Error("a chunk without usage replaced the referenced usage object")
				}
			case 3:
				last = current()
				if last == first {
					t.Error("a chunk with new usage mutated the old referenced object")
				}
			}
		},
	}
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "model"}}
	provider.parseSSE(t.Context(), reader, builder, nil)
	if current() != last {
		t.Error("terminalization replaced the last referenced usage object")
	}
}
