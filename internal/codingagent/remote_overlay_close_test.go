package codingagent

import (
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestRemoteOverlayClosesWithoutFurtherInput pins that an extension closing its
// own overlay ends the modal input loop immediately.
//
// A subprocess extension whose component returns Done sends ui.custom.close,
// which reaches the host as RemoteOverlayHandle.Close. That marks the overlay
// done and repaints, but RunRemoteOverlay's select waited only on the input
// channel and the stdin flush timer, so `!overlay.Done()` was not re-evaluated
// until the next keystroke arrived. The dialog stayed on screen and the tool
// stayed blocked; pressing any unrelated key flushed the already-pending close.
//
// The assertion is deliberately that no input is delivered after the close.
// A test that sends a key would pass against the broken code and prove nothing.
func TestRemoteOverlayClosesWithoutFurtherInput(t *testing.T) {
	// runCtx nil makes RunRemoteOverlay run setup/teardown inline instead of
	// handing them to the main input loop, which is not running here.
	m := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 120, 40)}
	u := &ExtUIContext{m: m}

	handleCh := make(chan extension.RemoteOverlayHandle, 1)
	done := make(chan any, 1)
	sawInput := make(chan struct{}, 1)

	go func() {
		result, _ := u.RunRemoteOverlay(
			extension.RemoteOverlayOptions{Title: "ask"},
			overlayInputSpy{onInput: func(string) {
				select {
				case sawInput <- struct{}{}:
				default:
				}
			}},
			func(h extension.RemoteOverlayHandle) { handleCh <- h },
		)
		done <- result
	}()

	var handle extension.RemoteOverlayHandle
	select {
	case handle = <-handleCh:
	case <-time.After(5 * time.Second):
		t.Fatal("overlay never opened")
	}

	// Drive one keystroke through the real modal input channel and wait for the
	// component to receive it. That proves the loop is parked in its select, so
	// the close below cannot race ahead of the loop ever starting.
	deliverModalInput(t, m, []byte("k"))
	select {
	case <-sawInput:
	case <-time.After(5 * time.Second):
		t.Fatal("modal input loop never delivered a keystroke")
	}

	// The extension resolves and closes. No further input is ever delivered.
	handle.Close("vermilion")

	select {
	case got := <-done:
		if got != "vermilion" {
			t.Errorf("overlay returned %v, want %q", got, "vermilion")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("overlay stayed open after the extension closed it: " +
			"the input loop only re-checks Done() when a key arrives, " +
			"so the dialog hangs until the user presses something unrelated")
	}
}

// overlayInputSpy reports each chunk the modal loop delivers to the component.
type overlayInputSpy struct{ onInput func(string) }

func (s overlayInputSpy) OnInput(data string) { s.onInput(data) }

func deliverModalInput(t *testing.T, m *InteractiveMode, buf []byte) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		m.modalInputMu.Lock()
		ch := m.modalInputCh
		m.modalInputMu.Unlock()
		if ch != nil {
			select {
			case ch <- buf:
				return
			case <-deadline:
				t.Fatal("modal input channel never accepted a chunk")
			}
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			t.Fatal("modal input channel was never armed")
		}
	}
}
