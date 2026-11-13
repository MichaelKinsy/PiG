package codingagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// BenchmarkWriteCardFrames is a write call as the harness's write:N scenario streams it through the interactive mode: a
// message_update per 16 characters of content, and a frame of the card (at 160 columns) every eight of them, as the 16 ms frame
// tick finds them 2 ms apart. One op is the whole stream; the metrics are per frame. With warm=off the highlight warm-up, which
// runs on the session's worker, is replaced by nothing, so the frames are what is measured.
func BenchmarkWriteCardFrames(b *testing.B) {
	for _, warm := range []string{"off", "on"} {
		for _, kb := range []int{50, 200} {
			b.Run(fmt.Sprintf("warm=%s/%dKB", warm, kb), func(b *testing.B) {
				if warm == "off" {
					previous := writeWarmHighlight
					writeWarmHighlight = func(string, string) bool { return true }
					b.Cleanup(func() { writeWarmHighlight = previous })
				}
				content := streamedWriteTypeScript(kb)
				const width, delta, perFrame = 160, 16, 8
				frames := 0
				b.ReportAllocs()
				for b.Loop() {
					b.StopTimer()
					m, _ := newTickRenderProbe(b, "regular")
					m.backgroundCtx, m.backgroundCancel = context.WithCancel(b.Context())
					partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_0", Name: "write"}}}
					message := func(received string) agent.AgentMessage {
						call := ai.ToolCall{ID: "call_0", Name: "write", Arguments: ai.JsonObject{"path": "src/long.ts", "content": received}}
						return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonPending}}
					}
					m.handleAgentEvent(agent.MessageStartEvent{Message: message("")})
					m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(""), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial}})
					card := m.toolByID["call_0"]
					if card == nil {
						b.Fatal("toolcall_start made no tool card")
					}
					b.StartTimer()
					frames = 0
					for end, n := delta, 1; ; end, n = end+delta, n+1 {
						end = min(end, len(content))
						m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(content[:end]), AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: content[max(0, end-delta):end], Partial: partial}})
						if n%perFrame == 0 || end == len(content) {
							card.Render(width)
							frames++
						}
						if end == len(content) {
							break
						}
					}
					m.backgroundTasks.Wait()
					b.StopTimer()
					if drawn := strings.Join(card.Render(width), "\n"); !strings.Contains(drawn, "more lines") {
						b.Fatalf("the card does not draw the streamed content:\n%s", drawn)
					}
					b.StartTimer()
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*frames), "ns/frame")
			})
		}
	}
}
