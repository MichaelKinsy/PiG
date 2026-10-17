//go:build !race

package subprocess

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// streamMessageUpdates prepares and sends n message_update deltas of chunk to one connection's stream, as the provider's accumulator publishes them: each text extends the bytes of the one before, in a buffer that grows by doubling.
func streamMessageUpdates(n int, chunk string) time.Duration {
	stream := &messageUpdateStream{}
	conn := &Conn{}
	var text strings.Builder
	start := time.Now()
	for range n {
		text.WriteString(chunk)
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text.String()}}, StopReason: ai.StopReasonStop}
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
	return time.Since(start)
}

// A message_update delta costs the delta, not the text so far: the long strings are validated as UTF-8 and compared with the previous payload's from where the previous payload ended (EX-F2). At base a5df22bd3a every delta rescanned the whole text about six times (the UTF-8 check of each of its two copies, the prefix comparison and the validity of both sides of the append), so a ten times longer stream cost about six times more per delta.
func TestMessageUpdateDeltaCostDoesNotGrowWithTheText(t *testing.T) {
	chunk := strings.Repeat("délta 🙂 ", 4)
	perDelta := func(n int) time.Duration {
		best := time.Duration(1<<63 - 1)
		for range 3 {
			best = min(best, streamMessageUpdates(n, chunk)/time.Duration(n))
		}
		return best
	}
	const short, long = 500, 5000
	shortCost, longCost := perDelta(short), perDelta(long)
	ratio := float64(longCost) / float64(shortCost)
	t.Logf("per delta: %d deltas %v, %d deltas %v, ratio %.2f", short, shortCost, long, longCost, ratio)
	testenv.TimingGate(t, ratio > 2.5, "a delta of a %d-delta stream costs %.2fx one of a %d-delta stream, want <= 2.5x: per-delta work grows with the text", long, ratio, short)
}
