package codingagent

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// captureUIBridge is a minimal SubprocessUIBridge that records the host actions
// wireSubprocessHostCallbacks registers, so a test can invoke one directly.
type captureUIBridge struct {
	actions map[string]any
}

func (b *captureUIBridge) SetInvalidate(func())                                     {}
func (b *captureUIBridge) SetNotifyFunc(func(string, string))                       {}
func (b *captureUIBridge) SetUIContext(extension.UIContext)                         {}
func (b *captureUIBridge) SetUIPromptScope(subprocess.UIPromptScope)                {}
func (b *captureUIBridge) PublishModelCatalog()                                     {}
func (b *captureUIBridge) SetWidgetSyncFunc(func(map[string]*subprocess.PushProxy)) {}
func (b *captureUIBridge) SetHostAction(key string, fn any) {
	if b.actions == nil {
		b.actions = map[string]any{}
	}
	b.actions[key] = fn
}

func newSendUserMessageHarness(t *testing.T) (*InteractiveMode, chan capturedStreamRequest, func(any, subprocess.SendUserMessageOptions) error) {
	t.Helper()
	seen := make(chan capturedStreamRequest, 1)
	model := &ai.Model{
		ID:           "capture-1",
		DisplayName:  "capture-1",
		Provider:     captureStreamOptionsProvider{seen: seen},
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	m.wireSubprocessHostCallbacks()

	fn, ok := bridge.actions["sendUserMessage"].(func(any, subprocess.SendUserMessageOptions) error)
	if !ok {
		t.Fatalf("sendUserMessage host action missing or wrong type: %T", bridge.actions["sendUserMessage"])
	}
	return m, seen, fn
}

// drainOneUITask runs the single closure the fix marshals onto the main loop.
func drainOneUITask(t *testing.T, m *InteractiveMode) {
	t.Helper()
	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("sendUserMessage did not post a UI task")
	}
}

// Upstream sendUserMessage routes through prompt(): while idle it STARTS a turn,
// it does not merely queue. pig used to always enqueue, so an extension that
// injected a message while the agent was idle stalled silently. This asserts the
// injected message reaches the provider (a turn started).
func TestInteractiveMode_SendUserMessage_IdleStartsTurn(t *testing.T) {
	m, seen, sendUserMessage := newSendUserMessageHarness(t)
	m.isIdle = true

	if err := sendUserMessage("interview me", subprocess.SendUserMessageOptions{DeliverAs: "followUp"}); err != nil {
		t.Fatalf("sendUserMessage returned error: %v", err)
	}
	drainOneUITask(t, m)

	select {
	case opts := <-seen:
		if got := lastUserMessageText(t, opts.Messages); got != "interview me" {
			t.Fatalf("turn started with wrong prompt: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle sendUserMessage did not start a turn: injected message was dropped")
	}
}

// While the agent is busy, sendUserMessage must queue (not start a second
// concurrent turn), matching upstream prompt(streamingBehavior=followUp).
func TestInteractiveMode_SendUserMessage_BusyQueues(t *testing.T) {
	m, seen, sendUserMessage := newSendUserMessageHarness(t)
	// Simulate an in-flight turn. turnActive is the signal that means a run
	// goroutine exists to drain the follow-up queue; isIdle alone describes the
	// window *after* a turn ends, where queueing strands the message.
	m.isIdle = false
	m.turnActive.Store(true)

	if err := sendUserMessage("later", subprocess.SendUserMessageOptions{DeliverAs: "followUp"}); err != nil {
		t.Fatalf("sendUserMessage returned error: %v", err)
	}
	drainOneUITask(t, m)

	select {
	case <-seen:
		t.Fatal("sendUserMessage started a concurrent turn while the agent was busy")
	case <-time.After(300 * time.Millisecond):
		// Expected: queued, no new Send.
	}
	if !m.agent.HasPendingMessages() {
		t.Fatal("busy sendUserMessage should enqueue the message as a follow-up")
	}
}

func TestInteractiveMode_InprocSendUserMessage_IdleStartsTurn(t *testing.T) {
	m, seen, _ := newSendUserMessageHarness(t)
	m.newRunner = inproc.NewRunner(nil, m.opts.CWD)
	m.wireInprocContextActions()
	cc := m.newRunner.CreateCommandContext()
	m.isIdle = true

	if err := cc.SendUserMessage("in-process onboarding", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err != nil {
		t.Fatalf("SendUserMessage() returned error: %v", err)
	}
	drainOneUITask(t, m)

	select {
	case opts := <-seen:
		if got := lastUserMessageText(t, opts.Messages); got != "in-process onboarding" {
			t.Fatalf("turn started with wrong prompt: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-process sendUserMessage did not start a turn")
	}
}

func TestSendUserMessageActiveInputHandledBeforeQueue(t *testing.T) {
	m, _, _ := newSendUserMessageHarness(t)
	m.turnActive.Store(true)
	called := false
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "input", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			called = true
			event := args[0].(extension.InputEvent)
			if event.Source != extension.InputSourceExtension || event.StreamingBehavior != string(extension.DeliverAsFollowUp) || len(event.Images) != 1 {
				t.Fatalf("input metadata = %#v", event)
			}
			image, ok := event.Images[0].(ai.ImageContent)
			if !ok || image.Data != "raw" || image.MimeType != "image/png" {
				t.Fatalf("input images = %#v", event.Images)
			}
			return extension.InputEventResultHandled{}, nil
		}},
	}}}, t.TempDir())

	content := ai.UserContentBlocks{ai.TextContent{Text: "intercept"}, ai.ImageContent{Data: "raw", MimeType: "image/png"}}
	if err := m.deliverUserMessage(content, extension.DeliverAsFollowUp); err != nil {
		t.Fatal(err)
	}
	drainOneUITask(t, m)
	if queued := m.agent.ClearFollowUpQueue(); !called || len(queued) != 0 {
		t.Fatalf("input handled hook called=%v, queued=%d", called, len(queued))
	}
}

func TestSendUserMessageIdleInputUsesExtensionSource(t *testing.T) {
	m, _, _ := newSendUserMessageHarness(t)
	var source extension.InputSource
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "input", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			source = args[0].(extension.InputEvent).Source
			return extension.InputEventResultHandled{}, nil
		}},
	}}}, t.TempDir())

	if err := m.deliverUserMessage(ai.UserContentBlocks{ai.TextContent{Text: "intercept"}}, extension.DeliverAsFollowUp); err != nil {
		t.Fatal(err)
	}
	drainOneUITask(t, m)
	if source != extension.InputSourceExtension {
		t.Fatalf("input source = %q", source)
	}
}

func TestSendUserMessageActiveInputTransformReplacesTextAndImages(t *testing.T) {
	m, _, _ := newSendUserMessageHarness(t)
	m.turnActive.Store(true)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "input", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(...any) (any, error) {
			return extension.InputEventResultTransform{
				Text:   "transformed",
				Images: []extension.ImageContent{ai.ImageContent{Data: "replacement", MimeType: "image/webp"}},
			}, nil
		}},
	}}}, t.TempDir())

	if err := m.deliverUserMessage("original", extension.DeliverAsSteer); err != nil {
		t.Fatal(err)
	}
	drainOneUITask(t, m)
	queued := m.agent.ClearSteeringQueue()
	if len(queued) != 1 || queued[0].User == nil || len(queued[0].User.Content) != 2 {
		t.Fatalf("queued = %#v", queued)
	}
	text, textOK := queued[0].User.Content[0].(ai.TextContent)
	image, imageOK := queued[0].User.Content[1].(ai.ImageContent)
	if !textOK || text.Text != "transformed" || !imageOK || image.Data != "replacement" || image.MimeType != "image/webp" {
		t.Fatalf("transformed content = %#v", queued[0].User.Content)
	}
}

func TestSendUserMessageIdleInputTransformReachesProvider(t *testing.T) {
	m, seen, _ := newSendUserMessageHarness(t)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "input", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(...any) (any, error) {
			return extension.InputEventResultTransform{
				Text: "transformed idle",
				// Unlike the active steer/follow-up queue path, starting a turn
				// from idle runs the same attachment size/decode validation as a
				// typed prompt, so this must be real, decodable image bytes or it
				// is replaced with an "Image omitted" placeholder before it
				// reaches the provider (internal/imageprocessing).
				Images: []extension.ImageContent{ai.ImageContent{Data: upstreamTinyPNG, MimeType: "image/png"}},
			}, nil
		}},
	}}}, t.TempDir())

	if err := m.deliverUserMessage("original", extension.DeliverAsFollowUp); err != nil {
		t.Fatal(err)
	}
	drainOneUITask(t, m)
	select {
	case request := <-seen:
		assertRequestUserContent(t, request, "transformed idle", upstreamTinyPNG, "image/png")
	case <-time.After(2 * time.Second):
		t.Fatal("transformed idle prompt did not reach provider")
	}
}

func assertRequestUserContent(t *testing.T, request capturedStreamRequest, wantText, wantData, wantMIME string) {
	t.Helper()
	for _, message := range request.Messages {
		user, ok := message.(ai.UserMessage)
		if !ok {
			continue
		}
		blocks, ok := user.Content.(ai.UserContentBlocks)
		if !ok || len(blocks) != 2 {
			t.Fatalf("provider user content = %#v", user.Content)
		}
		text, textOK := blocks[0].(ai.TextContent)
		image, imageOK := blocks[1].(ai.ImageContent)
		if !textOK || text.Text != wantText || !imageOK || image.Data != wantData || image.MimeType != wantMIME {
			t.Fatalf("provider user content = %#v", blocks)
		}
		return
	}
	t.Fatal("provider request omitted user message")
}

// TestSubprocessShutdownHostActionRequestsExitViaOwnerLoop guards the production
// cross-goroutine boundary that caused the original teardown race: the subprocess
// "shutdown" host action runs on a per-request host goroutine (off the owner
// loop). It must reach requestShutdown: set the atomic exit flag and post a wake
// : WITHOUT tearing down the renderer off-loop; the owner loop then observes the
// flag and performs teardown. Invoking the captured action from a separate
// goroutine mirrors the host dispatch.
func TestSubprocessShutdownHostActionRequestsExitViaOwnerLoop(t *testing.T) {
	spy := &stopSpyRenderer{TuiAltScreen: tui.NewTuiAltScreenWithOutput(io.Discard, 80, 24, tui.TuiAltScreenOptions{})}
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.tuiInst = spy
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	m.wireSubprocessHostCallbacks()

	shutdown, ok := bridge.actions["shutdown"].(func())
	if !ok {
		t.Fatalf("shutdown host action missing or wrong type: %T", bridge.actions["shutdown"])
	}

	done := make(chan struct{})
	go func() { shutdown(); close(done) }() // per-request host goroutine
	<-done

	if !m.requestExit.Load() {
		t.Error("subprocess shutdown host action did not request exit")
	}
	if spy.stops != 0 {
		t.Errorf("subprocess shutdown host action tore down the renderer off-loop (Stop=%d): teardown must be deferred to the owner loop", spy.stops)
	}

	// Owner-loop completion: the loop drains the wake, sees the flag, tears down.
	select {
	case fn := <-m.uiTaskCh:
		fn()
	default:
		t.Fatal("subprocess shutdown host action posted no wake for the owner loop")
	}
	if m.requestExit.Load() {
		m.stopInteractiveTui()
	}
	if spy.stops != 1 {
		t.Errorf("owner-loop teardown after the shutdown flag: renderer Stop=%d, want 1", spy.stops)
	}
}

// Upstream types sendUserMessage's delivery as {steer, followUp} and reserves
// nextTurn for sendMessage, which carries a custom message rather than a user
// turn (types.ts:1298 vs 1308). deliverUserMessage already matches that, and
// SendUserMessageOptions says so; nothing held it, and the neighbouring
// sendMessage path does accept nextTurn, so the two are one careless edit apart.
//
// A user message queued for the next turn is not one the agent will answer: it
// arrives alongside whatever the user sends next, so an extension asking a
// question gets silence until the user happens to speak.
func TestSendUserMessageRejectsNextTurnDelivery(t *testing.T) {
	_, _, sendUserMessage := newSendUserMessageHarness(t)

	err := sendUserMessage("queue me", subprocess.SendUserMessageOptions{DeliverAs: "nextTurn"})
	if err == nil {
		t.Fatal("nextTurn was accepted for a user message; upstream allows it only for sendMessage")
	}
	if !strings.Contains(err.Error(), "nextTurn") {
		t.Errorf("the error does not name the rejected mode: %v", err)
	}
}

// The two modes upstream does allow must keep working.
func TestSendUserMessageAcceptsTheUpstreamDeliveryModes(t *testing.T) {
	for _, mode := range []string{"steer", "followUp", ""} {
		t.Run("deliverAs="+mode, func(t *testing.T) {
			_, _, sendUserMessage := newSendUserMessageHarness(t)
			if err := sendUserMessage("hello", subprocess.SendUserMessageOptions{DeliverAs: mode}); err != nil {
				t.Errorf("deliverAs %q was rejected: %v", mode, err)
			}
		})
	}
}

// Upstream sendUserMessage is prompt(), which renders the user message only
// when the agent emits message_start for it. A ctx.ui.notify the handler makes
// right after pi.sendUserMessage (pi-msg-queue's /q) therefore appears before
// the user message. pig used to render the prompt eagerly when the main loop
// started the turn, which reversed the order.
func TestInteractiveMode_SendUserMessageRendersAfterSameHandlerNotify(t *testing.T) {
	m, seen, sendUserMessage := newSendUserMessageHarness(t)
	m.isIdle = true

	if err := sendUserMessage("remember the tests", subprocess.SendUserMessageOptions{DeliverAs: "followUp"}); err != nil {
		t.Fatalf("sendUserMessage returned error: %v", err)
	}
	drainOneUITask(t, m)
	m.showExtensionNotify("Follow-up message sent.", "info")
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("idle sendUserMessage did not start a turn")
	}
	if rendered := strings.Join(m.chatContainer.Render(100), "\n"); strings.Contains(rendered, "remember the tests") {
		t.Fatalf("user message rendered before the agent emitted message_start:\n%s", rendered)
	}
	m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{User: &agent.UserMessage{
		Role:    agent.RoleUser,
		Content: []ai.UserContentBlock{ai.TextContent{Text: "remember the tests"}},
	}}})

	rendered := strings.Join(m.chatContainer.Render(100), "\n")
	notice := strings.Index(rendered, "Follow-up message sent.")
	user := strings.Index(rendered, "remember the tests")
	if notice < 0 || user < 0 || notice > user {
		t.Fatalf("want the notify before the user message, got:\n%s", rendered)
	}
	if strings.Count(rendered, "remember the tests") != 1 {
		t.Fatalf("user message rendered more than once:\n%s", rendered)
	}
}
