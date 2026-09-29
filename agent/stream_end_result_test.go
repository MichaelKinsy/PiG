package agent

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// An extension may close iteration before resolving Result. Aborting that request must still settle the agent instead of leaving it blocked on the result Promise.
func TestConsumeEndedStreamStillOwnsResultCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := NewAgent(AgentOptions{})
		stream := ai.NewAssistantMessageEventStream()
		stream.End()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { _, _, err := a.consumeStream(ctx, stream, nil); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	})
}
