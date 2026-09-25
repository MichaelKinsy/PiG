package coding

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestSessionReplaceInnerPublishesModelToConcurrentObservers(t *testing.T) {
	model := fakeModel()
	model.Capabilities.ContextWindow = 128000
	// Restore may resolve a fresh model instance from the persisted selection;
	// observers must safely see either complete pointer during publication.
	session, err := NewSession(newTestServices(t), SessionOptions{Model: model, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	inner := session.Inner()
	message := &agent.AssistantMessage{Provider: model.Provider.ID(), StopReason: ai.StopReasonError, ErrorMessage: "overloaded"}
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		for range 500 {
			session.ReplaceInner(inner)
		}
	})
	workers.Go(func() {
		<-start
		for range 5000 {
			if got := session.Model(); got == nil || got.Provider == nil {
				t.Errorf("model snapshot is incomplete: %#v", got)
				return
			}
			_ = session.contextWindow()
			_ = session.AvailableThinkingLevels()
			_ = session.willRetryAfterAgentEnd(message)
		}
	})
	close(start)
	workers.Wait()
}
