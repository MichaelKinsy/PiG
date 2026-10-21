package codingagent

import (
	"context"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// newSendMessageHarness is an interactive mode whose agent streams for the test (turnActive is what the mode reads as upstream isStreaming) and the registered sendMessage host action, the entry point of every SDK's pi.sendMessage.
func newSendMessageHarness(t *testing.T, streaming bool) (*InteractiveMode, *recordingCompactHandle, func(extension.CustomMessageRef, extension.SendMessageOptions) error) {
	t.Helper()
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewFooterComponent(nil, "", nil)
	m.agent = mustNewAgent(agent.AgentOptions{})
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}
	handle := &recordingCompactHandle{agent: m.agent, inner: NewSession("send-message", t.TempDir())}
	m.opts.SessionHandle = handle
	handle.streaming = streaming
	m.turnActive.Store(streaming)
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	m.wireSubprocessHostCallbacks()
	action, ok := bridge.actions["sendMessage"].(func(extension.CustomMessageRef, subprocess.SendMessageOptions) error)
	if !ok {
		t.Fatalf("sendMessage host action missing or wrong type: %T", bridge.actions["sendMessage"])
	}
	return m, handle, func(msg extension.CustomMessageRef, opts extension.SendMessageOptions) error {
		return action(msg, subprocess.SendMessageOptions{TriggerTurn: opts.TriggerTurn, DeliverAs: string(opts.DeliverAs)})
	}
}

func customMessageTypes(messages []agent.AgentMessage) []string {
	var types []string
	for _, message := range messages {
		customType, _ := message.Custom["customType"].(string)
		types = append(types, customType)
	}
	return types
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Upstream sendCustomMessage (agent-session.ts:2336-2358) queues a streaming message on the steering queue unless deliverAs is "followUp", and
// holds it until the turn's tool results are complete when triggerTurn is exactly false. Interactive mode used to queue every streaming message as a
// follow-up, which a run delivers only when it would stop, and queued a triggerTurn false message the same way, which cost the run another
// model call.
func TestInteractiveSendMessageWhileStreamingRoutesAsUpstream(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name         string
		opts         extension.SendMessageOptions
		wantSteer    []string
		wantFollowUp []string
		wantDeferred []string
	}{
		{name: "no options steers", wantSteer: []string{"m"}},
		{name: "steer steers", opts: extension.SendMessageOptions{DeliverAs: extension.DeliverAsSteer}, wantSteer: []string{"m"}},
		{name: "unknown deliverAs steers", opts: extension.SendMessageOptions{DeliverAs: "later"}, wantSteer: []string{"m"}},
		{name: "followUp follows up", opts: extension.SendMessageOptions{DeliverAs: extension.DeliverAsFollowUp}, wantFollowUp: []string{"m"}},
		{name: "triggerTurn true steers", opts: extension.SendMessageOptions{TriggerTurn: &yes}, wantSteer: []string{"m"}},
		{name: "triggerTurn true with followUp follows up", opts: extension.SendMessageOptions{TriggerTurn: &yes, DeliverAs: extension.DeliverAsFollowUp}, wantFollowUp: []string{"m"}},
		{name: "triggerTurn false defers", opts: extension.SendMessageOptions{TriggerTurn: &no}, wantDeferred: []string{"m"}},
		{name: "triggerTurn false with followUp defers", opts: extension.SendMessageOptions{TriggerTurn: &no, DeliverAs: extension.DeliverAsFollowUp}, wantDeferred: []string{"m"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, handle, send := newSendMessageHarness(t, true)
			if err := send(extension.CustomMessageRef{CustomType: "m", Content: "x", Display: true}, tc.opts); err != nil {
				t.Fatal(err)
			}
			steering, followUps := m.agent.PendingMessages()
			if got := customMessageTypes(steering); !equalStrings(got, tc.wantSteer) {
				t.Errorf("steering queue = %v, want %v", got, tc.wantSteer)
			}
			if got := customMessageTypes(followUps); !equalStrings(got, tc.wantFollowUp) {
				t.Errorf("follow-up queue = %v, want %v", got, tc.wantFollowUp)
			}
			if got := customMessageTypes(handle.deferred); !equalStrings(got, tc.wantDeferred) {
				t.Errorf("deferred messages = %v, want %v", got, tc.wantDeferred)
			}
		})
	}
}

// A message held for the end of a turn never reaches the agent's queues, so it cannot start another model call.
func TestInteractiveSendMessageDeferredIsNotPending(t *testing.T) {
	no := false
	m, handle, send := newSendMessageHarness(t, true)
	if err := send(extension.CustomMessageRef{CustomType: "m", Content: "x"}, extension.SendMessageOptions{TriggerTurn: &no}); err != nil {
		t.Fatal(err)
	}
	if m.agent.HasQueuedMessages() {
		t.Fatal("a deferred custom message is queued on the agent")
	}
	if len(handle.deferred) != 1 {
		t.Fatalf("deferred = %d, want 1", len(handle.deferred))
	}
}

// With no run left to defer to, the message appends at once as upstream's final branch does.
func TestInteractiveSendMessageDeferFallsBackToAppendWhenRunEnded(t *testing.T) {
	no := false
	_, handle, send := newSendMessageHarness(t, true)
	handle.streaming = false
	if err := send(extension.CustomMessageRef{CustomType: "m", Content: "x"}, extension.SendMessageOptions{TriggerTurn: &no}); err != nil {
		t.Fatal(err)
	}
	if len(handle.deferred) != 0 {
		t.Fatalf("deferred = %d, want 0", len(handle.deferred))
	}
	entries := handle.inner.GetEntries()
	if len(entries) != 1 {
		t.Fatalf("session has %d entries, want the appended custom message", len(entries))
	}
}

// Upstream sendCustomMessage validates nothing: an empty customType is appended and an unknown deliverAs steers.
func TestInteractiveSendMessageAcceptsEmptyCustomTypeAndUnknownDeliverAs(t *testing.T) {
	_, _, send := newSendMessageHarness(t, false)
	if err := send(extension.CustomMessageRef{CustomType: "", Content: "x"}, extension.SendMessageOptions{}); err != nil {
		t.Fatalf("empty customType: %v", err)
	}
	_, _, send = newSendMessageHarness(t, true)
	if err := send(extension.CustomMessageRef{CustomType: "m", Content: "x"}, extension.SendMessageOptions{DeliverAs: "later"}); err != nil {
		t.Fatalf("unknown deliverAs: %v", err)
	}
}

// upstream: agent-session.ts:2443-2445 counts the queued user messages only.
func TestInteractiveHasPendingMessagesCountsUserMessagesOnly(t *testing.T) {
	m, _, send := newSendMessageHarness(t, true)
	if err := send(extension.CustomMessageRef{CustomType: "m", Content: "x"}, extension.SendMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err != nil {
		t.Fatal(err)
	}
	if err := send(extension.CustomMessageRef{CustomType: "m", Content: "x"}, extension.SendMessageOptions{}); err != nil {
		t.Fatal(err)
	}
	hasPending, ok := m.opts.SubprocessUIBridge.(*captureUIBridge).actions["hasPendingMessages"].(func() bool)
	if !ok {
		t.Fatal("hasPendingMessages host action missing")
	}
	inProc := m.extensionHasPendingMessages
	if hasPending() || inProc() {
		t.Fatal("queued custom messages count as pending")
	}
	m.followUpMessageWithImages("typed", []ai.ImageContent(nil))
	if !hasPending() || !inProc() {
		t.Fatal("a queued user message is not pending")
	}
}

// Messages deferred during a run are appended when the run ends, before the mode reports idle, and a message deferred while the end is settling is
// appended too.
func TestInteractiveSettleTurnFlushesDeferredCustomMessages(t *testing.T) {
	m, handle, _ := newSendMessageHarness(t, true)
	handle.deferred = []agent.AgentMessage{{Custom: map[string]any{"customType": "m"}}}
	if err := m.settleTurn(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(handle.flushedDeferred) != 1 || len(handle.deferred) != 0 {
		t.Fatalf("flushed %d, still deferred %d; want the message flushed", len(handle.flushedDeferred), len(handle.deferred))
	}
	if m.turnActive.Load() {
		t.Fatal("turn still active after settle")
	}
}
