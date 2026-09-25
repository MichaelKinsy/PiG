package codingagent

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// An extension's sendUserMessage is never dropped: upstream awaits prompt()
// on its event loop. Interactive mode posted it with a non-blocking send, so
// a full UI task queue discarded the message while the call returned nil
// (MODES-09, TUI-03).
func TestDeliverUserMessageIsNotDroppedWhenTheUIQueueIsFull(t *testing.T) {
	seen := make(chan capturedStreamRequest, 4)
	model := &ai.Model{ID: "capture-1", DisplayName: "capture-1", Provider: captureStreamOptionsProvider{seen: seen}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.editor = tui.NewEditor()
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	for cap(m.uiTaskCh) > len(m.uiTaskCh) {
		m.uiTaskCh <- func() {}
	}

	delivered := make(chan error, 1)
	go func() { delivered <- m.deliverUserMessage("from an extension", extension.DeliverAsFollowUp) }()
	select {
	case err := <-delivered:
		if err == nil {
			// Accepted without room in the queue: it can only have been dropped
			// unless the owner loop runs it below.
			delivered <- nil
		} else {
			t.Fatalf("deliverUserMessage failed while the loop is alive: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
	}

	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	select {
	case err := <-delivered:
		if err != nil {
			t.Fatalf("deliverUserMessage: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deliverUserMessage did not complete once the loop drained")
	}
	select {
	case request := <-seen:
		if got := lastUserMessageText(t, request.Messages); got != "from an extension" {
			t.Fatalf("provider prompt = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the extension's message never reached the model: it was dropped")
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.turnActive.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-loopDone
}

// Upstream prompt() throws when a run is active and sendUserMessage gave no
// deliverAs; interactive mode silently turned it into a follow-up.
func TestDeliverUserMessageWithoutDeliveryModeWhileStreamingFails(t *testing.T) {
	m, _, _ := newStreamingRoutingMode(t)
	err := m.deliverUserMessage("no mode", "")
	const want = "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."
	if err == nil || err.Error() != want {
		t.Fatalf("deliverUserMessage error = %v, want %q", err, want)
	}
	if steering, followUps := m.agent.PendingMessages(); len(steering)+len(followUps) != 0 {
		t.Fatalf("a rejected message was queued: steering=%d followUps=%d", len(steering), len(followUps))
	}
}
