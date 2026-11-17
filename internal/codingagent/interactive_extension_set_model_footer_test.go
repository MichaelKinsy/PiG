package codingagent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// heldModelSelectHandle is a Session whose pi.setModel changes the model at once, as Pi's setModel sets state.model before it awaits model_select
// (agent-session.ts:2644-2663), and then holds the model_select handlers' reply open until release is closed.
type heldModelSelectHandle struct {
	*recordingCompactHandle
	held    chan struct{}
	release chan struct{}
}

func (h *heldModelSelectHandle) ExtensionSetModelOnMain(ctx context.Context, model *ai.Model, dispatch func(func() error) error) (bool, error) {
	if err := dispatch(func() error { return h.SetModel(model) }); err != nil {
		return false, err
	}
	close(h.held)
	select {
	case <-h.release:
	case <-ctx.Done():
	}
	return true, nil
}

// Pi's footer reads session.state.model on every frame (footer.ts:107, 160, 220), and setModel sets it before it awaits the model_select handlers, so a frame drawn
// while a handler is still replying already shows the new model and its window. Pig's footer is pushed: the extension's setModel pushed it only after the handlers had
// replied, so a frame drawn in between (a key) came from the footer's cache with the old window, and nothing drew again. The model and the footer change together, on the
// owner loop, at the moment the Session's model changes; the handlers are awaited off the loop.
func TestInteractiveExtensionSetModelShowsTheNewWindowWhileItsHandlersAreStillReplying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		first := &ai.Model{ID: "first", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}, Capabilities: ai.ModelCapabilities{ContextWindow: 777000}}
		next := &ai.Model{ID: "next", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}, Capabilities: ai.ModelCapabilities{ContextWindow: 555000}}
		m.opts.Model = first
		m.statusLine.SetModel(first)
		m.tuiInst.Render()
		handle := &heldModelSelectHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}, held: make(chan struct{}), release: make(chan struct{})}
		unblock := sync.OnceFunc(func() { close(handle.release) })
		t.Cleanup(unblock)
		m.opts.SessionHandle = handle
		bridge := &captureUIBridge{}
		m.opts.SubprocessUIBridge = bridge
		m.opts.ModelBinder = func(object *ai.Model) *ai.Model { return object }
		detach := m.wireSubprocessHostCallbacks()
		defer detach()

		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		m.drainMainLoopOnce()
		if before := widthx.StripAnsi(term.take()); !strings.Contains(before, "/777k") {
			t.Fatalf("the frame before the selection lacks the first model's window /777k: %q", before)
		}

		type outcome struct {
			ok  bool
			err error
		}
		called := make(chan outcome, 1)
		go func() {
			ok, err := bridge.actions["setModel"].(func(context.Context, *ai.Model) (bool, error))(t.Context(), next)
			called <- outcome{ok, err}
		}()
		// Serve the owner loop until the Session's model has changed and the model_select handlers are replying.
		for range 20 {
			synctest.Wait()
			m.drainMainLoopOnce()
			select {
			case <-handle.held:
			default:
				continue
			}
			break
		}
		select {
		case <-handle.held:
		default:
			t.Fatal("the extension's setModel never reached its model_select handlers")
		}
		select {
		case got := <-called:
			t.Fatalf("setModel returned %+v while its handler was still replying", got)
		default:
		}

		// A key reaches the editor and the frame it draws is the next one.
		m.editor.HandleInput("x")
		m.tuiInst.Render()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		m.drainMainLoopOnce()
		frame := widthx.StripAnsi(term.take())
		if !strings.Contains(frame, "/555k") {
			t.Errorf("the frame drawn while the handler replies lacks the new model's window /555k: %q", frame)
		}
		if strings.Contains(frame, "/777k") {
			t.Errorf("the frame drawn while the handler replies still shows the old window /777k: %q", frame)
		}

		unblock()
		synctest.Wait()
		m.drainMainLoopOnce()
		select {
		case got := <-called:
			if got.err != nil || !got.ok {
				t.Fatalf("setModel = %+v", got)
			}
		default:
			t.Fatal("setModel did not return after its handler replied")
		}
		if len(handle.switched) != 1 || handle.switched[0] != next {
			t.Fatalf("Session got SetModel %v, want one SetModel of next", handle.switched)
		}
	})
}

// callSetModelAction runs the extension host's setModel action, which runs off the owner loop, on its own goroutine and serves the owner loop's tasks until it returns.
func callSetModelAction(t *testing.T, m *InteractiveMode, bridge *captureUIBridge, model *ai.Model) (bool, error) {
	t.Helper()
	type outcome struct {
		ok  bool
		err error
	}
	called := make(chan outcome, 1)
	go func() {
		ok, err := bridge.actions["setModel"].(func(context.Context, *ai.Model) (bool, error))(t.Context(), model)
		called <- outcome{ok, err}
	}()
	timeout := time.After(testbudget.Wait(t))
	for {
		select {
		case got := <-called:
			return got.ok, got.err
		case task := <-m.uiTaskCh:
			task()
		case <-timeout:
			t.Fatal("the setModel action never returned")
		}
	}
}
