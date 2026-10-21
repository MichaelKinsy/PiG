package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// BenchmarkParallelToolBatch runs one assistant message of eight parallel tool calls end to end: the batch creates a gate and a slot per
// call, and each call ends in settle order. "settled" is a tool whose result arrives from outside the process, so the goroutine that
// reads it settles its slot, as the subprocess host does.
func BenchmarkParallelToolBatch(b *testing.B) {
	const calls = 8
	for _, settled := range []bool{false, true} {
		name := "unsettled"
		if settled {
			name = "settled"
		}
		b.Run(name, func(b *testing.B) {
			tool := &scriptTool{name: "p", mode: ToolModeParallel, params: map[string]any{"type": "object"},
				execute: func(ctx context.Context, id string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
					if settled {
						if slot := ToolSettleFromContext(ctx, id); slot != nil {
							slot.Settle()
						}
					}
					return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: id}}}, nil
				}}
			batch := make([]ai.ToolCall, calls)
			for i := range batch {
				batch[i] = toolCall(fmt.Sprintf("call-%d", i), "p", nil)
			}
			provider := &scriptedProvider{respond: toolCallsThenText(batch...)}
			a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}})
			a.Subscribe(func(context.Context, AgentEvent) error { return nil })
			b.ReportAllocs()
			for b.Loop() {
				if _, err := a.Send(b.Context(), "run"); err != nil {
					b.Fatal(err)
				}
				if err := a.Reset(); err != nil {
					b.Fatal(err)
				}
				provider.requests = nil
			}
		})
	}
}
