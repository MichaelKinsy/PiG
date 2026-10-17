//go:build !race && linux

package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// A file written through a tool call streams its arguments into the interactive card 16 bytes at a time. Each delta must cost the same whatever came before it: the card reads the arguments received so far in place instead of a copy of all of them per delta. The gate compares the deltas after 1,600 and after 3,200, through the interactive event handler.
func TestInteractiveToolArgumentsPerDeltaAllocationGate(t *testing.T) {
	const n = 1600
	var source strings.Builder
	for line := 0; source.Len() < 16*4*n; line++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", line, line%97, line)
	}
	arguments, err := json.Marshal(map[string]string{"path": "src/long.ts", "content": source.String()})
	if err != nil {
		t.Fatal(err)
	}
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_0", Name: "write"}}}
	marginal := perfgate.CheckLinear(t, "interactive.tool_arguments_delta", n, 1.5, func(units int) {
		m, _ := newTickRenderProbe(t, "regular")
		for unit := range units {
			m.handleAgentEvent(agent.MessageUpdateEvent{AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: string(arguments[16*unit : 16*unit+16]), Partial: partial}})
		}
		if m.toolByID["call_0"] == nil {
			t.Fatal("the deltas made no tool card")
		}
	})
	perfgate.Check(t, "interactive.tool_arguments_delta.allocs", marginal.Allocs)
	perfgate.Check(t, "interactive.tool_arguments_delta.bytes", marginal.Bytes)
}
