package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestHarness drives an InteractiveMode's owner loop without a terminal, for
// tests in package codingagent_test that pair the mode with a real
// coding.Session. The loop runs posted UI tasks and handles Session events
// like inputLoop; every other access goes through Do, on that loop.
type TestHarness struct {
	m       *InteractiveMode
	ctx     context.Context
	onEvent func(h *TestHarness, ev agent.AgentEvent)
}

// NewTestHarness builds a mode around opts.SessionHandle and starts its owner
// loop. onEvent, when non-nil, runs on the loop after the mode handles each
// Session event.
func NewTestHarness(t testing.TB, opts InteractiveOptions, onEvent func(h *TestHarness, ev agent.AgentEvent)) *TestHarness {
	t.Helper()
	m := NewInteractiveMode(opts)
	m.chatContainer = tui.NewContainer()
	m.loadedResourcesContainer = tui.NewContainer()
	m.editorContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.rendererOut = io.Discard
	m.tuiInst = tui.NewWithOutput(m.rendererOut, 100, 30)
	m.statusLine = NewStatusLine(opts.Model, "", nil)
	m.editor = tui.NewEditor()
	m.editorContainer.Add(m.editor)
	m.layout = tui.NewContainer(m.loadedResourcesContainer, m.chatContainer, m.editorContainer)
	m.tuiInst.SetFocus(m.editor)
	m.newRunner = opts.ExtensionRunner
	m.wireInprocContextActions()
	m.keybindings = DefaultKeybindingsManager()
	m.slashRegistry = NewSlashRegistry()
	m.agent = opts.SessionHandle.Agent()
	m.installRunPromptTurnRefresh()
	m.eventCh = opts.SessionHandle.Events()
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	h := &TestHarness{m: m, ctx: ctx, onEvent: onEvent}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case fn := <-m.uiTaskCh:
				fn()
			case ev, ok := <-m.eventCh:
				if !ok {
					m.eventCh = nil
					continue
				}
				m.handleAgentEvent(ev)
				if h.onEvent != nil {
					h.onEvent(h, ev)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return h
}

// Do runs fn on the owner loop and waits for it.
func (h *TestHarness) Do(fn func()) {
	finished := make(chan struct{})
	h.m.runOnMain(h.ctx, func() {
		fn()
		close(finished)
	})
	<-finished
}

// Enter types text into the editor and presses Enter. Call it from an onEvent
// callback, which already runs on the owner loop, or through Do.
func (h *TestHarness) Enter(text string) {
	h.m.editor.SetText(text)
	_ = h.m.dispatchKey(h.ctx, "\r")
}

// SubmitInitialMessages runs the startup path for positional messages after
// the first. Call it off the owner loop.
func (h *TestHarness) SubmitInitialMessages(messages []string) {
	h.m.submitInitialMessages(h.ctx, messages)
}

// Key dispatches one key sequence. Call it on the owner loop, like Enter.
func (h *TestHarness) Key(data string) {
	_ = h.m.dispatchKey(h.ctx, data)
}

// EditorText returns the editor's text.
func (h *TestHarness) EditorText() string {
	var text string
	h.Do(func() { text = h.m.editor.Text() })
	return text
}

// Idle reports, on the owner loop, whether no run is active.
func (h *TestHarness) Idle() bool {
	var idle bool
	h.Do(func() { idle = !h.m.turnActive.Load() && h.m.isIdle })
	return idle
}

// WaitForSessionIdle drives the production captured-session waiter off the owner loop.
func (h *TestHarness) WaitForSessionIdle(ctx context.Context) error { return h.m.waitForIdle(ctx) }

// SetIdleWaitState installs the independent mode cleanup barrier and Session on the owner loop.
func (h *TestHarness) SetIdleWaitState(session InteractiveSessionHandle, settled chan struct{}) {
	h.m.queueMu.Lock()
	h.m.opts.SessionHandle = session
	h.m.turnSettled = settled
	h.m.queueMu.Unlock()
}

// WaitIdle waits until no run is active and the UI shows idle.
func (h *TestHarness) WaitIdle(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !h.Idle() {
		if time.Now().After(deadline) {
			t.Fatal("interactive run did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Notify and ChatOnOwner are used inside Do callbacks without posting recursively.
func (h *TestHarness) Notify(message string) { h.m.showExtensionNotify(message, "info") }
func (h *TestHarness) ChatOnOwner() string {
	lines := h.m.chatContainer.Render(100)
	for i := range lines {
		lines[i] = widthx.StripAnsi(lines[i])
	}
	return strings.Join(lines, "\n")
}

// Chat returns the transcript as plain text.
func (h *TestHarness) Chat() string {
	var chat string
	h.Do(func() {
		lines := h.m.chatContainer.Render(100)
		for i, line := range lines {
			lines[i] = widthx.StripAnsi(line)
		}
		chat = strings.Join(lines, "\n")
	})
	return chat
}

// QueuedMessages reports the agent's queued steering and follow-up counts.
func (h *TestHarness) QueuedMessages() (steering, followUp int) {
	h.Do(func() {
		s, f := h.m.agent.PendingMessages()
		steering, followUp = len(s), len(f)
	})
	return steering, followUp
}

// ToolCard builds the pending card of one tool call, bound to definition as the interactive mode binds a registered definition (ToolExecutionComponent constructed with a definition), and returns its Render.
func ToolCard(t *testing.T, name, toolCallID string, args json.RawMessage, definition extension.ToolDefinition) func(width int) []string {
	t.Helper()
	return toolComponentRaw(t, name, toolCallID, args, &definition).card.Render
}
