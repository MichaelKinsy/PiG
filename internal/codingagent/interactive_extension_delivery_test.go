package codingagent

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// An extension's sendUserMessage is never dropped: upstream awaits prompt()
// on its event loop. Interactive mode posted it with a non-blocking send, so
// a full UI task queue discarded the message while the call returned nil
// (MODES-09, TUI-03).
func TestDeliverUserMessageIsNotDroppedWhenTheUIQueueIsFull(t *testing.T) {
	seen := make(chan capturedStreamRequest, 4)
	model := &ai.Model{ID: "capture-1", DisplayName: "capture-1", Provider: captureStreamOptionsProvider{seen: seen}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewFooterComponent(model, "", nil)
	m.editor = tui.NewEditor()
	m.agent = mustNewAgent(agent.AgentOptions{Model: model})
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

// Upstream prompt() throws when a run is active and sendUserMessage gave no deliverAs (agent-session.ts:2240-2244); interactive mode silently
// turned it into a follow-up, then returned the refusal to the extension. Pi's sendUserMessage is void and its bound action reports the refusal
// as the extension error `send_user_message` (:3565-3572), as the Session's binding does in print, JSON and RPC: the host action, which every SDK's
// call reaches, returns nil, the error event is emitted once with Pi's text, and nothing is queued.
func TestSendUserMessageWithoutDeliveryModeWhileStreamingIsReportedNotReturned(t *testing.T) {
	m, _, _ := newStreamingRoutingMode(t)
	m.newRunner = inproc.NewRunner(nil, t.TempDir())
	var reported []extension.ExtensionError
	m.newRunner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, *err) })
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	m.wireSubprocessHostCallbacks()
	send, ok := bridge.actions["sendUserMessage"].(func(any, subprocess.SendUserMessageOptions) error)
	if !ok {
		t.Fatalf("sendUserMessage host action missing or wrong type: %T", bridge.actions["sendUserMessage"])
	}

	if err := send("no mode", subprocess.SendUserMessageOptions{}); err != nil {
		t.Fatalf("the host action returned %v, want nil: Pi's call returns and the refusal is an extension error", err)
	}
	const want = "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."
	if len(reported) != 1 || reported[0].Event != "send_user_message" || reported[0].ExtensionPath != "<runtime>" || reported[0].Error != want {
		t.Fatalf("extension errors = %+v, want one send_user_message error %q", reported, want)
	}
	if steering, followUps := m.agent.PendingMessages(); len(steering)+len(followUps) != 0 {
		t.Fatalf("a rejected message was queued: steering=%d followUps=%d", len(steering), len(followUps))
	}
	if err := send("queued", subprocess.SendUserMessageOptions{DeliverAs: "followUp"}); err != nil {
		t.Fatalf("a followUp send returned %v", err)
	}
	if len(reported) != 1 {
		t.Fatalf("a followUp send was reported: %+v", reported)
	}
}

// Pi's sendMessage is void as well: the Promise it starts that rejects is reported as the extension error `send_message` (agent-session.ts:3556-3564),
// so the host action returns nil in interactive mode as the Session's binding does. A Session that is not there is the failure the harness has.
func TestSendMessageFailureIsReportedNotReturned(t *testing.T) {
	m, _, _ := newSendUserMessageHarness(t)
	m.newRunner = inproc.NewRunner(nil, t.TempDir())
	var reported []extension.ExtensionError
	m.newRunner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, *err) })
	send, ok := m.opts.SubprocessUIBridge.(*captureUIBridge).actions["sendMessage"].(func(extension.CustomMessageRef, subprocess.SendMessageOptions) error)
	if !ok {
		t.Fatal("sendMessage host action missing or wrong type")
	}
	if err := send(extension.CustomMessageRef{CustomType: "notice", Display: true}, subprocess.SendMessageOptions{}); err != nil {
		t.Fatalf("the host action returned %v, want nil: Pi's call returns and a failure is an extension error", err)
	}
	if len(reported) != 1 || reported[0].Event != "send_message" || reported[0].ExtensionPath != "<runtime>" || reported[0].Error == "" {
		t.Fatalf("extension errors = %+v, want one send_message error", reported)
	}
}
