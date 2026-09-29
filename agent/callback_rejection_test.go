package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-loop.ts awaits prepareRequest and finishTurn; agent.ts:519-553 catches their rejection and emits the same synthetic failed-turn lifecycle.
func TestRequestAndFinishTurnRejectionsUseFailedTurnLifecycle(t *testing.T) {
	for _, hook := range []string{"prepareRequest", "finishTurn"} {
		t.Run(hook, func(t *testing.T) {
			provider := &scriptedProvider{respond: replyText("completed")}
			failure := errors.New(hook + " failed")
			entered, release := make(chan struct{}), make(chan struct{})
			reject := func() error { close(entered); <-release; return failure }
			options := AgentOptions{Model: scriptedModel(provider)}
			if hook == "prepareRequest" {
				options.PrepareRequest = func(context.Context, PrepareRequestContext) (*AgentRequestUpdate, error) { return nil, reject() }
			} else {
				options.FinishTurn = func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) { return nil, reject() }
			}
			a := NewAgent(options)
			events := []string{}
			a.Subscribe(func(_ context.Context, event AgentEvent) error {
				switch event := event.(type) {
				case MessageStartEvent:
					if event.Message.Assistant != nil && event.Message.Assistant.ErrorMessage == failure.Error() {
						events = append(events, "message_start")
					}
				case MessageEndEvent:
					if event.Message.Assistant != nil && event.Message.Assistant.ErrorMessage == failure.Error() {
						events = append(events, "message_end")
					}
				case TurnEndEvent:
					events = append(events, "turn_end")
				case AgentEndEvent:
					events = append(events, "agent_end")
				}
				return nil
			})
			done := make(chan error, 1)
			go func() { _, err := a.Send(t.Context(), "start"); done <- err }()
			<-entered
			select {
			case err := <-done:
				t.Fatalf("prompt returned before its callback: %v", err)
			default:
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(events, []string{"message_start", "message_end", "turn_end", "agent_end"}) {
				t.Fatalf("failure lifecycle=%v", events)
			}
			messages := a.Messages()
			last := messages[len(messages)-1].Assistant
			if last == nil || last.ErrorMessage != failure.Error() || last.StopReason != ai.StopReasonError {
				t.Fatalf("failed assistant=%+v", last)
			}
			wantCalls := 0
			if hook == "finishTurn" {
				wantCalls = 1
			}
			if got := provider.calls(); got != wantCalls {
				t.Fatalf("provider calls=%d, want %d", got, wantCalls)
			}
		})
	}
}
