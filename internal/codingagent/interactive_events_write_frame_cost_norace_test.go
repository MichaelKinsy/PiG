//go:build !race

package codingagent

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// A streamed write card draws again on every frame while its arguments arrive (Pi's updateArgs on each message_update). A
// frame must cost what it draws plus the delta it adds, not the arguments received so far: the card reads the partial
// arguments the streaming parser holds and the write renderer extends what it drew last. The frames are measured once 40 KB
// and once 400 KB of content have arrived; a frame at 400 KB may not allocate more than 1.5 times what one at 40 KB does.
func TestStreamedWriteCardFrameCostIsFlat(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	const width, delta, warm, frames = 120, 64, 8, 64
	var source strings.Builder
	for line := 0; source.Len() < 400<<10+(warm+frames+1)*delta; line++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", line, line%7, line)
	}
	content := source.String()
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_0", Name: "write"}}}
	message := func(received string) agent.AgentMessage {
		call := ai.ToolCall{ID: "call_0", Name: "write", Arguments: ai.JsonObject{"path": "src/long.ts", "content": received}}
		return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonPending}}
	}
	m.handleAgentEvent(agent.MessageStartEvent{Message: message("")})
	m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(""), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial}})
	card := m.toolByID["call_0"]
	if card == nil {
		t.Fatal("toolcall_start made no tool card")
	}
	var last []string
	measure := func(received int) (allocs, bytes float64) {
		updates := make([]agent.MessageUpdateEvent, warm+frames)
		for i := range updates {
			end := received + i*delta
			updates[i] = agent.MessageUpdateEvent{Message: message(content[:end]), AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: content[end-delta : end], Partial: partial}}
		}
		frame := func(update agent.MessageUpdateEvent) {
			m.handleAgentEvent(update)
			last = card.Render(width)
		}
		for _, update := range updates[:warm] {
			frame(update)
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for _, update := range updates[warm:] {
			frame(update)
		}
		runtime.ReadMemStats(&after)
		return float64(after.Mallocs-before.Mallocs) / frames, float64(after.TotalAlloc-before.TotalAlloc) / frames
	}
	shortAllocs, shortBytes := measure(40 << 10)
	longAllocs, longBytes := measure(400 << 10)
	if drawn := strings.Join(last, "\n"); !strings.Contains(drawn, "export") || !strings.Contains(drawn, "more lines") {
		t.Fatalf("the card does not draw the streamed content:\n%s", drawn)
	}
	t.Logf("per frame: %.1f allocs %.0f B at 40 KB, %.1f allocs %.0f B at 400 KB", shortAllocs, shortBytes, longAllocs, longBytes)
	if longAllocs > 1.5*shortAllocs || longBytes > 1.5*shortBytes {
		t.Fatalf("a frame costs more as the arguments grow: %.1f allocs %.0f B at 40 KB, %.1f allocs %.0f B at 400 KB", shortAllocs, shortBytes, longAllocs, longBytes)
	}
}
