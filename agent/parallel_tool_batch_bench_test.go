package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// One run whose answer is a batch of parallel tool calls, after a history of messages. Every call of the batch receives the messages
// of the run itself for its tool and its after hook (executeToolCallsParallel), as the calls of a batch in Pi share one context, so
// the bytes of a run do not grow by the size of the history for every call: the rows show a batch after 100 and after 5,000 messages.
func BenchmarkParallelToolBatchWithHistory(b *testing.B) {
	for _, calls := range []int{2, 8} {
		for _, history := range []int{100, 5000} {
			b.Run(fmt.Sprintf("calls=%d/history=%d", calls, history), func(b *testing.B) {
				toolCalls := make([]ai.ToolCall, calls)
				for i := range toolCalls {
					toolCalls[i] = toolCall(fmt.Sprintf("tool-%d", i), "echo", ai.JsonObject{"value": "v"})
				}
				messages := make([]AgentMessage, history)
				for i := range messages {
					messages[i] = userMessage("m")
				}
				b.ReportAllocs()
				for b.Loop() {
					provider := &scriptedProvider{respond: toolCallsThenText(toolCalls...)}
					a := mustNewAgent(AgentOptions{
						Model:         scriptedModel(provider),
						Tools:         []AgentTool{valueEchoTool(ToolModeParallel, nil)},
						ToolExecution: ToolModeParallel,
					})
					a.SetMessages(messages)
					if _, err := a.Send(context.Background(), "echo"); err != nil {
						b.Fatal(err)
					}
					if provider.calls() != 2 {
						b.Fatalf("model requests %d, want 2", provider.calls())
					}
				}
			})
		}
	}
}
