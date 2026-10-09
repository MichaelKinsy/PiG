package codingagent

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestInputHandlerConfirmDialogAnswersThroughTheOwnerLoop is a black-box
// regression for the frozen terminal: an extension input handler that calls
// ui.confirm must have its dialog installed by the owner loop while it waits,
// the user's keys must reach that dialog, and the confirmed prompt must reach
// the provider. It drives only Enter and the real ExtUIContext.Confirm, so it
// does not depend on how input-handler dispatch is implemented.
func TestInputHandlerConfirmDialogAnswersThroughTheOwnerLoop(t *testing.T) {
	seen := make(chan capturedStreamRequest, 4)
	model := &ai.Model{
		ID:           "confirm-1",
		DisplayName:  "confirm-1",
		Provider:     captureStreamOptionsProvider{seen: seen},
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := newSwitchTuiProbe(t)
	m.opts.Model = model
	m.keybindings = otherColumnKeys()
	m.slashRegistry = NewSlashRegistry()
	m.agent = mustNewAgent(agent.AgentOptions{Model: model})

	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	t.Cleanup(func() {
		// Cancelling runCtx also releases a handler stuck in the dialog, so a
		// wedged loop unwinds and the test reports instead of hanging.
		cancel()
		<-loopDone
		m.teardownCurrentTui()
		m.tuiInst.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	})

	ui := &ExtUIContext{m: m}
	answered := make(chan bool, 1)
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "confirmer", Handlers: map[string][]extension.HandlerFn{
		EventInput: {func(args ...any) (any, error) {
			ok, err := ui.Confirm(ctx, "Send this prompt?", "", extension.ExtensionUIDialogOptions{})
			answered <- ok
			if err != nil {
				return nil, err
			}
			if !ok {
				return extension.InputEventResultHandled{}, nil
			}
			return nil, nil
		}},
	}}}, t.TempDir())

	// onOwnerLoop runs fn on the owner loop and reports whether the loop got to
	// it in time. A wedged loop never runs it.
	onOwnerLoop := func(fn func()) bool {
		done := make(chan struct{})
		go func() {
			_ = m.postToMain(ctx, func() { fn(); close(done) })
		}()
		select {
		case <-done:
			return true
		case <-time.After(2 * time.Second):
			return false
		}
	}

	// Enter is delivered on the owner loop and deliberately not waited for:
	// a loop wedged inside its own Enter handling is the failure under test.
	go func() {
		_ = m.postToMain(ctx, func() {
			m.editor.SetText("are you sure?")
			_ = m.dispatchKey(ctx, "\r")
		})
	}()

	// The loop must come back and show the dialog while the handler waits.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var installed bool
		if !onOwnerLoop(func() { installed = m.extensionDialog != nil }) {
			t.Fatal("WEDGED: the owner loop stopped serving after Enter; the input handler's confirm dialog was never installed")
		}
		if installed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the input handler's confirm dialog was never installed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Enter on the dialog picks the highlighted "Yes".
	if !onOwnerLoop(func() { _ = m.dispatchKey(ctx, "\r") }) {
		t.Fatal("the owner loop stopped serving while the dialog was open")
	}
	select {
	case ok := <-answered:
		if !ok {
			t.Fatal("confirm returned No for the highlighted Yes")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the confirm dialog never returned the user's answer")
	}

	select {
	case request := <-seen:
		if got := lastUserMessageText(t, request.Messages); got != "are you sure?" {
			t.Fatalf("provider prompt = %q, want the confirmed input", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the confirmed prompt never reached the provider")
	}
}
