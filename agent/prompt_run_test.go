package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent.ts:503-517,561-610 clears run error before agent_start and commits prompt messages only at message_end. Splitting admission from execution must preserve both boundaries.
func TestPromptRunStartPreservesCompletedTranscript(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return errorStream(ai.StopReasonError) }}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})
	mustSend(t, a, "fail first")
	if a.ErrorMessage() != "error" {
		t.Fatalf("initial failure state = %q", a.ErrorMessage())
	}
	provider.respond = replyText("done")
	before := roles(a.MessagesSnapshot())
	var observed []string
	var startError string
	starts := 0
	a.Subscribe(func(_ context.Context, event AgentEvent) error {
		if _, ok := event.(AgentStartEvent); ok {
			starts++
			observed = roles(a.MessagesSnapshot())
			startError = a.ErrorMessage()
		}
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	run, err := a.BeginSendContent(ctx, []ai.UserContentBlock{ai.TextContent{Text: "next"}})
	if err != nil {
		t.Fatal(err)
	}
	signal := a.Signal()
	startErr := run.Start()
	held := roles(a.MessagesSnapshot())
	_, runErr := run.Run()
	if startErr != nil || runErr != nil {
		t.Fatalf("start=%v run=%v", startErr, runErr)
	}
	if starts != 1 || !slices.Equal(observed, before) || !slices.Equal(held, before) || startError != "" {
		t.Fatalf("start count=%d transcript=%v held=%v error=%q; want one start with completed transcript %v and cleared error", starts, observed, held, startError, before)
	}
	if got, want := roles(a.MessagesSnapshot()), slices.Concat(before, []string{RoleUser, RoleAssistant}); !slices.Equal(got, want) {
		t.Fatalf("settled transcript=%v, want %v", got, want)
	}
	cancel()
	if signal == nil || signal.Err() != nil || a.Signal() != nil {
		t.Fatalf("successful claimed run did not detach its retained signal: %v", signal)
	}
}
