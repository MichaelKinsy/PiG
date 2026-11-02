//go:build !race

package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// reasoning_details are replay metadata: each delta is merged into the entry list in memory and the signature is
// serialized once, at thinking_end. A per-delta marshal makes every delta cost the whole signature so far.
func TestScalingOpenAIReasoningDetailsPerDelta(t *testing.T) {
	for _, kind := range []string{"encrypted", "merged text"} {
		testscaling.Check(t, testscaling.Case{Name: "openai-completions reasoning_details, " + kind, Size: 300, Bound: testscaling.Linear, Prepare: func(n int) func() {
			body := openAIReasoningDetailsBody(n, strings.Fields(kind)[0])
			provider := &openAIProvider{cfg: OpenAIConfig{ProviderID: "openrouter"}}
			return func() {
				builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openrouter", "model")
				provider.parseSSE(context.Background(), strings.NewReader(body), builder, nil)
				if message := builder.stream.Result(); message.StopReason != StopReasonStop {
					panic("stop reason " + string(message.StopReason) + ": " + message.ErrorMessage)
				}
			}
		}})
	}
}

func BenchmarkPAOpenAIReasoningDetails(b *testing.B) {
	for _, kind := range []string{"encrypted", "mergedtext"} {
		for _, n := range []int{10, 100, 1000, 10000} {
			body := openAIReasoningDetailsBody(n, kind)
			b.Run(fmt.Sprintf("%s/n=%d", kind, n), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					provider := &openAIProvider{cfg: OpenAIConfig{ProviderID: "openrouter"}}
					builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openrouter", "model")
					provider.parseSSE(context.Background(), strings.NewReader(body), builder, nil)
					_ = builder.stream.Result()
				}
			})
		}
	}
}
