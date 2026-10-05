package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// An extension input handler that asks the user something is the normal case: Pi's
// own extensions ask before running a tool on a destructive path, and pi-tweaks'
// model guard asks before sending a prompt to a model the user did not choose.
//
// Pig dispatched input handlers on the goroutine that owns the terminal, and
// every dialog is installed on that same goroutine (ExtUIContext.runDialog posts
// through runOnMain and then waits for the answer). A handler that asked
// therefore waited for the loop that was waiting for the handler: Enter did
// nothing, no frame changed, and no key did anything again, with no error
// anywhere to explain it.
//
// This reproduces that shape with an in-process handler: it asks by posting to
// the owner loop and waiting for the reply, which is what runDialog does.

// waitForPromptDispatch waits until every submitted prompt has had its input
// handlers dispatched and their result applied on the owner loop. A harness with
// no loop of its own must drain uiTaskCh for this to finish.
func waitForPromptDispatch(t *testing.T, m *InteractiveMode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.preflightPending() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.preflightPending() {
		t.Fatal("the prompt never finished dispatching on the owner loop")
	}
}

// runPromptDispatch is waitForPromptDispatch for a harness that runs no owner
// loop of its own: it drains the loop's task queue, which is where a dispatched
// prompt's result is applied.
func runPromptDispatch(t *testing.T, m *InteractiveMode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.preflightPending() {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-time.After(5 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt never finished dispatching on the owner loop")
		}
	}
}

// newQuestionAskingMode builds an idle mode whose provider records the prompts it
// is asked to run, with an owner loop draining posted work.
func newQuestionAskingMode(t *testing.T) (*InteractiveMode, context.Context, chan capturedStreamRequest) {
	t.Helper()
	seen := make(chan capturedStreamRequest, 4)
	model := &ai.Model{
		ID:           "ask-1",
		DisplayName:  "ask-1",
		Provider:     captureStreamOptionsProvider{seen: seen},
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := newPendingDisplayHarness(t)
	m.opts.Model = model
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.slashRegistry = NewSlashRegistry()
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	t.Cleanup(func() {
		cancel()
		<-loopDone
	})
	return m, ctx, seen
}

// askThroughTheLoop is a dialog: the handler posts to the owner loop and waits
// for it to answer, exactly as ExtUIContext.runDialog does. installed, when not
// nil, is signalled as the loop installs the dialog.
func askThroughTheLoop(ctx context.Context, m *InteractiveMode, reply bool, installed chan<- struct{}) (any, error) {
	answer := make(chan bool, 1)
	if err := m.postToMain(ctx, func() {
		if installed != nil {
			installed <- struct{}{}
		}
		answer <- reply
	}); err != nil {
		return nil, err
	}
	select {
	case got := <-answer:
		if !got {
			return extension.InputEventResultHandled{}, nil
		}
	case <-time.After(3 * time.Second):
		return nil, errors.New("the dialog was never installed: the owner loop is blocked")
	}
	return nil, nil
}

// TestInputHandlerQuestionDoesNotWedgeTheOwnerLoop is the regression for the
// frozen terminal: a prompt whose input handler asks a question must have that
// question installed by the owner loop while the handler waits, and the prompt
// must reach the provider once it is answered.
//
// Before the fix the loop was inside the handler while the handler waited for the
// loop, so nothing ever installed the question and Enter did nothing at all.
func TestInputHandlerQuestionDoesNotWedgeTheOwnerLoop(t *testing.T) {
	m, ctx, seen := newQuestionAskingMode(t)
	installed := make(chan struct{}, 1)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "asker", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			return askThroughTheLoop(ctx, m, true, installed)
		}},
	}}}, t.TempDir())

	if !m.inputHandlersRegistered() {
		t.Fatal("the input handler is not registered, so this test would not exercise the dispatch")
	}
	// Posted to the owner loop, as Enter is, and deliberately not waited for: a
	// submission that blocked its own loop is exactly the failure under test, so
	// waiting for it here would hide the wedge instead of exposing it.
	if err := m.postToMain(ctx, func() { m.handleSubmit(ctx, "are you sure?") }); err != nil {
		t.Fatalf("submit: %v", err)
	}

	select {
	case <-installed:
	case <-time.After(2 * time.Second):
		t.Fatal("the owner loop never installed the input handler's question: Enter wedged the terminal")
	}
	waitForPromptDispatch(t, m)

	select {
	case request := <-seen:
		if got := lastUserMessageText(t, request.Messages); got != "are you sure?" {
			t.Fatalf("prompt = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answered prompt never reached the provider")
	}
}

// A declined question consumes the prompt: upstream reports the input as handled,
// so nothing is sent to the model.
func TestInputHandlerDeclinedQuestionSendsNothing(t *testing.T) {
	m, ctx, seen := newQuestionAskingMode(t)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "asker", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			return askThroughTheLoop(ctx, m, false, nil)
		}},
	}}}, t.TempDir())

	onLoop(m, ctx, func() { m.handleSubmit(ctx, "are you sure?") })
	waitForPromptDispatch(t, m)

	select {
	case request := <-seen:
		t.Fatalf("a declined question still sent %q to the provider", lastUserMessageText(t, request.Messages))
	case <-time.After(200 * time.Millisecond):
	}
}

// Two submissions made back to back reach the extensions in the order they were
// typed, one dispatch at a time, and neither is lost: the second is delivered as
// a steering message into the turn the first started, which is what upstream does
// with a prompt submitted while a run is active.
func TestPromptDispatchSerialisesSubmissionsInOrder(t *testing.T) {
	m, ctx, seen := newQuestionAskingMode(t)
	var order []string
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "asker", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			event := args[0].(extension.InputEvent)
			order = append(order, event.Text)
			return askThroughTheLoop(ctx, m, true, nil)
		}},
	}}}, t.TempDir())

	onLoop(m, ctx, func() {
		m.handleSubmit(ctx, "first")
		m.handleSubmit(ctx, "second")
	})

	var delivered []string
	deadline := time.After(5 * time.Second)
	for len(delivered) < 2 {
		select {
		case request := <-seen:
			for _, message := range request.Messages {
				if user, isUser := message.(ai.UserMessage); isUser {
					if text := textOf(t, user.Content); text != "" {
						delivered = append(delivered, text)
					}
				}
			}
		case <-deadline:
			t.Fatalf("the provider saw %v, want both prompts", delivered)
		}
	}
	waitForPromptDispatch(t, m)

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("input event order = %v, want the typed order", order)
	}
	if delivered[0] != "first" || delivered[1] != "second" {
		t.Fatalf("provider saw %v, want [first second]", delivered)
	}
}

// textOf renders one user message's text content.
func textOf(t *testing.T, content ai.UserContent) string {
	t.Helper()
	switch typed := content.(type) {
	case ai.UserText:
		return string(typed)
	case ai.UserContentBlocks:
		var parts []string
		for _, block := range typed {
			if text, isText := block.(ai.TextContent); isText {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}
