package subprocess

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkNodeTurnEndEvent is the turn_end dispatch to a Node extension whose handler edits nothing: its reply carries the boundary members alone.
func BenchmarkNodeTurnEndEvent(b *testing.B) {
	benchTurnEnd(b, `export default function(pi){let calls=0;pi.on("turn_end",()=>{calls++})}`)
}

// BenchmarkNodeTurnEndEventEdit is the same dispatch to a handler that edits the turn's assistant message in place, so its reply carries the edit beside the boundary members.
func BenchmarkNodeTurnEndEventEdit(b *testing.B) {
	benchTurnEnd(b, `export default function(pi){let n=2;pi.on("turn_end",(event)=>{event.message.timestamp=n++})}`)
}

func benchTurnEnd(b *testing.B, source string) {
	b.Helper()
	handler := loadNodeBenchExtension(b, "turn_end", source)
	assistant := &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hello"}}, Timestamp: 1}
	b.ReportAllocs()
	for b.Loop() {
		event := extension.TurnEndEvent{Type: "turn_end", Message: agent.AgentMessage{Assistant: assistant}, ToolResults: []extension.ToolResultMessage{}, BoundaryState: &extension.BoundaryState{}}
		if _, err := handler(event, b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNodeMessageUpdateStream is the message_update dispatch of one streamed response (about 2,000 deltas) to a Node extension whose handler edits nothing: the per-delta
// cost of the delta stream end to end, the figure the cost of a detection on that path is measured against.
func BenchmarkNodeMessageUpdateStream(b *testing.B) {
	provider := ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: new(4), Max: new(4)}})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
		ai.FauxText(strings.Repeat("a response streamed in many chunks, ", 950)),
	}})})
	var events []extension.MessageUpdateEvent
	a, err := agent.NewAgent(agent.AgentOptions{
		Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}},
		DefaultStreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return model.Provider.Stream(ctx, transcript, options)
		},
		OnEvent: func(ev agent.AgentEvent) {
			if update, ok := ev.(agent.MessageUpdateEvent); ok {
				events = append(events, extension.MessageUpdateEvent{Type: "message_update", Message: update.Message, AssistantMessageEvent: update.AssistantMessageEvent})
			}
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := a.Prompt(b.Context(), "go"); err != nil {
		b.Fatal(err)
	}
	handler := loadNodeBenchExtension(b, "message_update", `export default function(pi){let calls=0;pi.on("message_update",()=>{calls++})}`)
	b.ReportAllocs()
	for b.Loop() {
		for _, event := range events {
			if _, err := handler(event, b.Context()); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(events)), "ns/delta")
	b.ReportMetric(float64(len(events)), "deltas")
}

// agentEndMessages is the 200-message run the agent_end benchmarks hand a handler: a user prompt and an assistant answer, alternating.
const agentEndMessages = 200

// BenchmarkNodeAgentEndEvent is the agent_end dispatch of a run of 200 messages to a Node extension whose handler edits nothing: the cost of its reply when the SDK has to tell that the 200 messages are unchanged.
func BenchmarkNodeAgentEndEvent(b *testing.B) {
	benchAgentEnd(b, `export default function(pi){let calls=0;pi.on("agent_end",()=>{calls++})}`)
}

// BenchmarkNodeAgentEndEventEdit is the same dispatch to a handler that edits the first message in place.
func BenchmarkNodeAgentEndEventEdit(b *testing.B) {
	benchAgentEnd(b, `export default function(pi){let n=0;pi.on("agent_end",(event)=>{event.messages[0].content="edit "+(n++)})}`)
}

func benchAgentEnd(b *testing.B, source string) {
	b.Helper()
	handler := loadNodeBenchExtension(b, "agent_end", source)
	messages := make([]agent.AgentMessage, agentEndMessages)
	seqs := make([]uint64, agentEndMessages)
	for i := range messages {
		if i%2 == 0 {
			messages[i] = agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("question %d", i)), Timestamp: int64(i)}}
		} else {
			messages[i] = agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, Timestamp: int64(i)}}
		}
		seqs[i] = uint64(9000 + i)
	}
	ctx := extension.WithMessageSequences(context.Background(), func() []uint64 { return seqs })
	b.ReportAllocs()
	for b.Loop() {
		if _, err := handler(extension.AgentEndEvent{Type: "agent_end", Messages: messages}, ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNodeMessageStartEvent is the message_start dispatch of a user message to a Node extension whose handler edits nothing.
func BenchmarkNodeMessageStartEvent(b *testing.B) {
	benchMessageStart(b, `export default function(pi){let calls=0;pi.on("message_start",()=>{calls++})}`)
}

// BenchmarkNodeMessageStartEventEdit is the same dispatch to a handler that edits the user message in place.
func BenchmarkNodeMessageStartEventEdit(b *testing.B) {
	benchMessageStart(b, `export default function(pi){let n=0;pi.on("message_start",(event)=>{event.message.timestamp=2+n++})}`)
}

func benchMessageStart(b *testing.B, source string) {
	b.Helper()
	handler := loadNodeBenchExtension(b, "message_start", source)
	message := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1}}
	ctx := extension.WithMessageSequences(context.Background(), func() []uint64 { return []uint64{9001} })
	dispatch := func() {
		if _, err := handler(extension.MessageStartEvent{Type: "message_start", Message: message}, ctx); err != nil {
			b.Fatal(err)
		}
	}
	steadyAllocs(b, dispatch)
	b.ReportAllocs()
	for b.Loop() {
		dispatch()
	}
}

// steadyAllocs makes allocs/op independent of b.N and of when a collection falls. The first dispatches allocate what the connection keeps afterwards (about 560 allocations
// at 1x, so 600 allocs/op at b.N=1 and 47 at 100x against 42 at 1s), and the frame, response and write-result pools (sync.Pool) are emptied by every collection: one that lands
// inside a 5x run refills them, 3 allocs/op more than a run it misses. So the benchmark dispatches a few times first, then keeps the collector off for the measured loop and
// restores it at the end.
func steadyAllocs(b *testing.B, dispatch func()) {
	b.Helper()
	for range 3 {
		dispatch()
	}
	previous := debug.SetGCPercent(-1)
	b.Cleanup(func() { debug.SetGCPercent(previous) })
}
