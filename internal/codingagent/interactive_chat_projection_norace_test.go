//go:build !race

package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Each assistant delta updates the chat block from the message's blocks, stop reason and error text. The update reads them in place: its allocations do not grow with the tool call arguments the message carries, which a copy of the whole message per delta would clone (TU-T7).
func TestUpdateAssistantMessageBlockAllocationsIgnoreArguments(t *testing.T) {
	allocs := func(arguments int) float64 {
		args := ai.JsonObject{}
		for i := range arguments {
			args[fmt.Sprintf("key%d", i)] = map[string]any{"value": strings.Repeat("v", 64), "index": float64(i)}
		}
		message := &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonPending, Content: []ai.AssistantContentBlock{
			ai.ThinkingContent{Thinking: "plan"},
			ai.TextContent{Text: "writing the file"},
			ai.ToolCall{ID: "call_0", Name: "write", Arguments: args},
		}}
		block := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		return testing.AllocsPerRun(100, func() { updateAssistantMessageBlock(block, message) })
	}
	small, large := allocs(1), allocs(256)
	t.Logf("allocations per update: %v with 1 tool argument, %v with 256", small, large)
	if large != small {
		t.Fatalf("updating the block allocates %v times with 256 tool arguments, %v with 1; want the same", large, small)
	}
}
