//go:build !race

package subprocess

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gate for the message_update frames the host sends one extension process once per streamed delta (message_update_delta.go). The payload carries the whole partial message twice, as message and as the assistant event's partial (agent-session.ts:641-647); the host sends the first whole and each later one as the operations that turn the previous into it, so a streamed message of n deltas costs O(n) allocations and bytes. Pi hands in-process handlers the same objects by reference.
func TestScalingMessageUpdatePayload(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "message_update frames for an n-delta message", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		chunk := strings.Repeat("word ", 80)
		full := strings.Repeat(chunk, n)
		return func() {
			stream := &messageUpdateStream{}
			conn := &Conn{}
			for i := range n {
				text := full[:len(chunk)*(i+1)]
				partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop}
				event := extension.MessageUpdateEvent{
					Type:                  "message_update",
					Message:               agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: partial.Content, StopReason: ai.StopReasonStop}},
					AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: chunk, Partial: partial},
				}
				prepared, inStream, err := stream.prepare(conn, event)
				if err != nil || !inStream {
					panic(fmt.Sprint("message_update was not prepared as a stream frame: ", err))
				}
				stream.sent(conn, prepared, nil)
			}
		}
	}})
}
