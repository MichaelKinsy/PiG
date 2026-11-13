package codingagent_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// A provider that delivers one chunk at a time makes a task of each chunk, and a task of one message_update has no bounds to keep: the
// update goes on Events as it is. Two markers for each chunk woke the consumer three times where it woke once, which cost the interactive
// mode about 2 % of its instructions while a response streamed. A task of several events keeps its markers, a listener receives what it
// received, and without the markers Events holds the same events in the same order.
func TestATaskOfOneMessageUpdatePutsNoMarkerOnEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", TokensPerSecond: 400})
		provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{StopReason: string(ai.StopReasonStop), Content: []ai.FauxContentBlock{
			ai.FauxText("an answer that the provider streams in several chunks of text, one chunk at a time"),
		}})})
		log := sessionEventLogOn(t, provider, func(ctx context.Context, session *coding.Session) error {
			_, err := session.RunAgentPrompt(jsloop.WithTasks(ctx), func(ctx context.Context) ([]agent.AgentMessage, error) {
				return session.Agent().Send(ctx, "hello")
			})
			return err
		})
		consumer := withPrefix(log, "E:")
		const update = "E:agent.MessageUpdateEvent"
		opened, alone, marked := -1, 0, 0
		for i, entry := range consumer {
			switch entry {
			case "E:{":
				if opened >= 0 {
					t.Fatalf("a task opens inside another at %d: %q", i, consumer)
				}
				opened = i
			case "E:}":
				if opened < 0 {
					t.Fatalf("a task closes at %d that did not open: %q", i, consumer)
				}
				if i-opened == 2 && consumer[i-1] == update {
					t.Fatalf("the task of one message_update at %d is marked: %q", opened, consumer)
				}
				if i-opened < 2 {
					t.Fatalf("the markers at %d bound no event: %q", opened, consumer)
				}
				opened = -1
				marked++
			case update:
				if opened < 0 {
					alone++
				}
			}
		}
		if opened >= 0 {
			t.Fatalf("the task that opens at %d does not close: %q", opened, consumer)
		}
		if alone < 4 {
			t.Fatalf("%d message_update events are on Events without markers, want one for most chunks of the response: %q", alone, consumer)
		}
		// The loop's end events are one task of several events: message_end, turn_end and agent_end behind the last chunk.
		if marked == 0 || !strings.Contains(strings.Join(consumer, " "), "E:agent.MessageEndEvent E:agent.TurnEndEvent E:agent.AgentEndEvent E:}") {
			t.Fatalf("the end of the run is not one marked task: %q", consumer)
		}
		listener, events := withPrefix(log, "L:"), withPrefix(log, "E:agent.")
		for i := range listener {
			listener[i] = "E:" + strings.TrimPrefix(listener[i], "L:")
		}
		if !slices.Equal(listener, events) {
			t.Fatalf("the listener received %q, the consumer of Events %q: want the same events in the same order", listener, events)
		}
	})
}
