package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.99.1 agent-loop.ts:409 wraps the stream result, `const result = async () => Object.assign(await response.result(), { thinkingLevel })`, and the done/error branch awaits it (:445). The wrapper settles one reaction after its inner await, so message_end reaches listeners one reaction later than 0.87.1's `await response.result()`.
// The oracle is a real 0.99.1 Agent (pi-agent-core) whose stream function pushes `start` synchronously and then runs `await null; log(tick)` per round, with text_start after tick08 and the terminal event after tick12. Pi logs the same sequence for `done` and `error`:
//
//	push start | tick01 | tick02 | tick03 | LISTENER message_start | tick04 .. tick08 | push text_start | tick09 | tick10 | LISTENER message_update | tick11 | tick12 | push done | tick13 | tick14 | tick15 | tick16 | LISTENER message_end | tick17 .. tick20
//
// Pi 0.87.1 logs the listener's message_end after tick15.
func TestAgentMessageEndWaitsForTheResultWrapperReaction(t *testing.T) {
	want := []string{"push start", "tick01", "tick02", "tick03", "LISTENER message_start", "tick04", "tick05", "tick06", "tick07", "tick08", "push text_start", "tick09", "tick10", "LISTENER message_update", "tick11", "tick12", "push done", "tick13", "tick14", "tick15", "tick16", "LISTENER message_end", "tick17", "tick18", "tick19", "tick20"}
	for _, terminal := range []ai.StopReason{ai.StopReasonStop, ai.StopReasonError} {
		t.Run(string(terminal), func(t *testing.T) {
			var mu sync.Mutex
			var log []string
			record := func(entry string) {
				mu.Lock()
				defer mu.Unlock()
				log = append(log, entry)
			}
			produced := make(chan struct{})
			streamFn := func(runContext context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				stream := ai.NewAssistantMessageEventStream()
				partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, API: "pi-messages", Provider: "p", Model: model.ID, StopReason: ai.StopReasonPending, Timestamp: 1}
				if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
					return nil, err
				}
				record("push start")
				// The reserved continuation is the reaction after the synchronous prefix, as the producer's first `await null`.
				producer := ai.StreamObservationFromContext(runContext).PrepareContinuation()
				go func() {
					defer close(produced)
					_ = producer.Run(func(observation *ai.StreamObservation) error {
						for i := 1; i <= 12; i++ {
							if i > 1 {
								observation.Yield()
							}
							record(fmt.Sprintf("tick%02d", i))
							if i == 8 {
								partial.Content = append(partial.Content, ai.TextContent{Text: "a"})
								if err := stream.Push(ai.TextStartEvent{ContentIndex: 0, Partial: partial}); err != nil {
									return err
								}
								record("push text_start")
							}
						}
						final := *partial
						final.StopReason = terminal
						var err error
						if terminal == ai.StopReasonError {
							final.ErrorMessage = "x"
							err = stream.Push(ai.ErrorEvent{Reason: terminal, Error: &final})
						} else {
							err = stream.Push(ai.DoneEvent{Reason: terminal, Message: &final})
						}
						record("push done")
						for i := 13; i <= 20; i++ {
							observation.Yield()
							record(fmt.Sprintf("tick%d", i))
						}
						return err
					})
				}()
				return stream, nil
			}
			a := NewAgent(AgentOptions{StreamFn: streamFn})
			a.Subscribe(func(_ context.Context, event AgentEvent) error {
				switch event := event.(type) {
				case MessageStartEvent:
					if event.Message.Assistant != nil {
						record("LISTENER message_start")
					}
				case MessageUpdateEvent:
					record("LISTENER message_update")
				case MessageEndEvent:
					if event.Message.Assistant != nil {
						record("LISTENER message_end")
					}
				}
				return nil
			})
			if _, err := a.Send(t.Context(), "probe"); err != nil {
				t.Fatal(err)
			}
			<-produced
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(log, want) {
				t.Fatalf("event order differs from Pi 0.99.2\n got: %s\nwant: %s", strings.Join(log, " | "), strings.Join(want, " | "))
			}
		})
	}
}
