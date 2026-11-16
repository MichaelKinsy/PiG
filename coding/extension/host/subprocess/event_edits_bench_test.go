package subprocess

import (
	"context"
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
