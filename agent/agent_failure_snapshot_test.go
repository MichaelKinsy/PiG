package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// Upstream agent.ts handleRunFailure appends its failure message from the single
// event-loop owner. Go readers use MessagesSnapshot concurrently (the cache
// warmer's current-context check), so the failure append must take the same
// lock as ordinary appends. Run under -race.
func TestMessagesSnapshotSafeDuringRunFailureAppend(t *testing.T) {
	writeErr := errors.New("persist failure")
	provider := &scriptedProvider{respond: replyText("ok")}
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		OnMessagePersist: func(message AgentMessage) error {
			if message.Assistant != nil {
				return writeErr
			}
			return nil
		},
	})
	stop := make(chan struct{})
	started := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.MessagesSnapshot()
			}
		}
	})
	<-started
	for range 32 {
		if _, err := a.Send(context.Background(), "go"); !errors.Is(err, writeErr) {
			t.Errorf("Send error = %v, want %v", err, writeErr)
		}
	}
	close(stop)
	readers.Wait()
	last := a.MessagesSnapshot()
	if n := len(last); n == 0 || last[n-1].Assistant == nil || last[n-1].Assistant.ErrorMessage == "" {
		t.Fatalf("last message = %+v, want the appended failure message", last)
	}
}
