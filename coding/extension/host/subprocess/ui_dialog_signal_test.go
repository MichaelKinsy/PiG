package subprocess

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// dialogProbeUI answers every select by waiting for its context, so a dialog ends only when it is dismissed.
type dialogProbeUI struct {
	extension.UIContext
	mu      sync.Mutex
	started map[string]chan struct{}
	ended   map[string]chan struct{}
	// release ends every dialog still open when the test does, so none outlives it.
	release chan struct{}
}

func newDialogProbeUI() *dialogProbeUI {
	return &dialogProbeUI{UIContext: extension.NoopUIContext, started: map[string]chan struct{}{}, ended: map[string]chan struct{}{}, release: make(chan struct{})}
}

func (u *dialogProbeUI) channels(title string) (started, ended chan struct{}) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started[title] == nil {
		u.started[title], u.ended[title] = make(chan struct{}), make(chan struct{})
	}
	return u.started[title], u.ended[title]
}

func (u *dialogProbeUI) Select(ctx context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	started, ended := u.channels(title)
	close(started)
	select {
	case <-ctx.Done():
	case <-u.release:
	}
	close(ended)
	return "", ctx.Err()
}

// Pi's RPC dialogs resolve their default when options.signal aborts and stop waiting for a response (rpc-mode.ts createDialogPromise), and the interactive
// dialogs close. The Node runtime names such a dialog by id and sends ui.cancelDialog when the signal aborts: the host dismisses that dialog only, including
// when the cancel is read before the dialog's handler has started.
func TestDialogCancelDismissesOnlyTheNamedDialog(t *testing.T) {
	host := newWireHost(t)
	ui := newDialogProbeUI()
	t.Cleanup(func() { close(ui.release) })
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	host.SetUIBridge(bridge)
	w := newWireExt(t)
	if _, err := loadWireExt(t, host, w, &RegisterPayload{Name: "dialogs"}); err != nil {
		t.Fatal(err)
	}
	type selectArgs struct {
		Title    string   `json:"title"`
		Options  []string `json:"options"`
		DialogID string   `json:"dialogId,omitempty"`
	}
	signalled, err := w.startCall("ui.select", selectArgs{Title: "signalled", Options: []string{"a"}, DialogID: "d1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := w.startCall("ui.select", selectArgs{Title: "plain", Options: []string{"a"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := w.startCall("ui.select", selectArgs{Title: "other", Options: []string{"a"}, DialogID: "d2"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// The cancel follows the call on the wire but runs in a lane of its own, so it may run before the dialog's handler starts.
	if cancelled := w.call("ui.cancelDialog", map[string]string{"dialogId": "d1"}, ""); cancelled.Error != nil {
		t.Fatalf("cancel: %+v", cancelled.Error)
	}
	select {
	case result := <-signalled:
		var answer struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(result.Result, &answer); err != nil || answer.OK {
			t.Fatalf("the dismissed dialog answered %s (%v), want ok:false", result.Result, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ui.cancelDialog did not dismiss the dialog")
	}
	for _, title := range []string{"plain", "other"} {
		started, ended := ui.channels(title)
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatalf("the %s dialog never started", title)
		}
		select {
		case <-ended:
			t.Fatalf("cancelling d1 dismissed the %s dialog", title)
		default:
		}
	}
	if cancelled := w.call("ui.cancelDialog", map[string]string{"dialogId": "d2"}, ""); cancelled.Error != nil {
		t.Fatalf("cancel: %+v", cancelled.Error)
	}
	select {
	case <-other:
	case <-time.After(10 * time.Second):
		t.Fatal("ui.cancelDialog d2 did not dismiss the dialog")
	}
	select {
	case <-plain:
		t.Fatal("a dialog with no id was dismissed")
	default:
	}
}
