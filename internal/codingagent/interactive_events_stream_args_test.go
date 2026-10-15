package codingagent

import (
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// The card receives a streaming call's partial arguments on every delta, but a delta must cost the same however much of the
// arguments has arrived: the handler hands the card the parsed arguments the provider's parser left in the message, and the
// card encodes them only when it draws. The deltas are measured at 1 KiB and at 512 KiB of received content.
func TestInteractiveStreamedToolArgsDeltaCostIsFlat(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	content := strings.Repeat("export function step(input: number[]): number { return input.length; }\n", 512<<10/72)
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_0", Name: "write"}}}
	message := func(received string) agent.AgentMessage {
		call := ai.ToolCall{ID: "call_0", Name: "write", Arguments: ai.JsonObject{"path": "src/long.ts", "content": received}}
		return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{call}}}
	}
	m.handleAgentEvent(agent.MessageStartEvent{Message: message("")})
	m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(""), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial}})
	if m.toolByID["call_0"] == nil {
		t.Fatal("toolcall_start made no tool card")
	}
	measure := func(received int) (allocs, bytes float64) {
		const runs = 200
		update := agent.MessageUpdateEvent{Message: message(content[:received]), AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: content[received-16 : received], Partial: partial}}
		m.handleAgentEvent(update)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for range runs {
			m.handleAgentEvent(update)
		}
		runtime.ReadMemStats(&after)
		return float64(after.Mallocs-before.Mallocs) / runs, float64(after.TotalAlloc-before.TotalAlloc) / runs
	}
	shortAllocs, shortBytes := measure(1 << 10)
	longAllocs, longBytes := measure(len(content))
	t.Logf("per delta: %.1f allocs %.0f B at 1 KiB, %.1f allocs %.0f B at %d KiB", shortAllocs, shortBytes, longAllocs, longBytes, len(content)>>10)
	if longAllocs > shortAllocs+1 || longBytes > 1.5*shortBytes+256 {
		t.Fatalf("a delta costs more as the arguments grow: %.1f allocs %.0f B at 1 KiB, %.1f allocs %.0f B at %d KiB", shortAllocs, shortBytes, longAllocs, longBytes, len(content)>>10)
	}
}
