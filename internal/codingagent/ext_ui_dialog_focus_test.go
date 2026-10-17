package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 1.2.0 interactive-mode.ts:2690-2869 with tui.ts:598-665: an extension dialog that opens while a focused custom component holds the
// terminal shows at once and takes focus (showExtension*: this.ui.setFocus(dialog)). The overlay's focus restore is then blocked by the
// dialog, and hiding the dialog (this.ui.setFocus(this.editor) once the slot no longer holds it) hands focus back to the overlay, which
// receives the next key. An editor-slot custom component is displaced by the dialog's editorContainer.clear(), so hiding the dialog
// focuses the editor.

// startDialogOver opens a selector while a custom component is shown and pumps the owner loop until it is installed.
func startDialogOver(t *testing.T, r *customRig, title string) <-chan dialogCall {
	t.Helper()
	result := make(chan dialogCall, 1)
	r.workers.Go(func() {
		value, err := r.u.Select(r.m.runCtx, title, []string{"one", "two"}, extension.ExtensionUIDialogOptions{})
		result <- dialogCall{value, err}
	})
	r.pump(t, nil, func() bool { return r.m.extensionDialog != nil })
	return result
}

func requireDialogFocus(t *testing.T, m *InteractiveMode) {
	t.Helper()
	if focused := m.tuiInst.GetFocusedComponent(); focused != m.extensionDialog.component {
		t.Fatalf("focus while the dialog is shown = %T, want the dialog (interactive-mode.ts:2727 this.ui.setFocus(this.extensionSelector))", focused)
	}
}

// Host-process extension: ctx.ui.custom({overlay: true}), then ctx.ui.select.
func TestExtensionDialogTakesFocusFromFocusedCustomOverlay(t *testing.T) {
	r := newCustomRig(t)
	component := &customProbe{label: "OVERLAY"}
	var done func(any)
	out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return component, nil
	}, &extension.CustomOptions{Overlay: true})
	r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component })

	selected := startDialogOver(t, r, "Pick")
	requireDialogFocus(t, r.m)
	for _, key := range []string{"\x1b[B", "\r"} {
		if err := r.m.dispatchKey(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if got := requireResult(t, selected, "selector"); got.err != nil || got.value != "two" {
		t.Fatalf("selector = %q, %v; want two", got.value, got.err)
	}
	if got := component.received(); len(got) != 0 {
		t.Fatalf("the custom overlay received %q while the dialog was shown", got)
	}
	if focused := r.m.tuiInst.GetFocusedComponent(); any(focused) != component {
		t.Fatalf("focus after the dialog closed = %T, want the custom overlay", focused)
	}
	if err := r.m.dispatchKey(t.Context(), "x"); err != nil {
		t.Fatal(err)
	}
	if got := component.received(); len(got) != 1 || got[0] != "x" {
		t.Fatalf("custom overlay input after the dialog closed = %q, want x", got)
	}
	go done("closed")
	if got := r.finish(t, out); got.err != nil || got.value != "closed" {
		t.Fatalf("custom = %v, %v", got.value, got.err)
	}
}

// Host-process extension: ctx.ui.custom() in the editor slot, then ctx.ui.select. The dialog clears the slot, so hiding it focuses the editor.
func TestExtensionDialogDisplacesFocusedEditorSlotCustom(t *testing.T) {
	r := newCustomRig(t)
	component := &customProbe{label: "CUSTOM"}
	var done func(any)
	out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return component, nil
	}, nil)
	r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component })

	selected := startDialogOver(t, r, "Pick")
	requireDialogFocus(t, r.m)
	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, selected, "selector"); got.err != nil || got.value != "one" {
		t.Fatalf("selector = %q, %v; want one", got.value, got.err)
	}
	if focused := r.m.tuiInst.GetFocusedComponent(); focused != r.m.editor {
		t.Fatalf("focus after the dialog closed = %T, want the editor (interactive-mode.ts:2743)", focused)
	}
	if got := component.received(); len(got) != 0 {
		t.Fatalf("the displaced custom component received %q", got)
	}
	go done("closed")
	if got := r.finish(t, out); got.err != nil || got.value != "closed" {
		t.Fatalf("custom = %v, %v", got.value, got.err)
	}
}

// Subprocess extension: its ctx.ui.custom({overlay: true}) mounts through RunRemoteOverlay, whose loop reads the terminal while the overlay
// has focus. A dialog takes the terminal from it, and the overlay reads it again once the dialog closes.
func TestExtensionDialogTakesTerminalFromFocusedRemoteOverlay(t *testing.T) {
	r := newCustomRig(t)
	ctx := r.m.runCtx
	handles := make(chan extension.RemoteOverlayHandle, 1)
	inputs := make(chan string, 4)
	type remoteResult struct {
		value any
		ok    bool
	}
	remote := make(chan remoteResult, 1)
	r.workers.Go(func() {
		value, ok := r.u.RunRemoteOverlay(extension.RemoteOverlayOptions{Overlay: true}, overlayInputSpy{onInput: func(data string) { inputs <- data }}, func(handle extension.RemoteOverlayHandle) {
			handle.UpdateLines([]string{"OVERLAY"})
			handles <- handle
		})
		remote <- remoteResult{value, ok}
	})
	var overlay extension.RemoteOverlayHandle
	r.pump(t, nil, func() bool {
		select {
		case overlay = <-handles:
			return true
		default:
			return false
		}
	})

	// route sends one key through the input pump and reports whether it reached the main loop, where the driver dispatches it, or the
	// remote overlay.
	route := func(key string) (toMainLoop bool) {
		t.Helper()
		readCh := make(chan inputChunk, 1)
		r.workers.Go(func() { r.m.routeInputChunk(ctx, []byte(key), readCh) })
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case chunk := <-readCh:
				if err := r.m.dispatchKey(ctx, string(chunk.data)); err != nil {
					t.Fatal(err)
				}
				return true
			case got := <-inputs:
				if got != key {
					t.Fatalf("remote overlay input = %q, want %q", got, key)
				}
				return false
			case task := <-r.m.uiTaskCh:
				task()
			case <-timer.C:
				t.Fatalf("key %q reached neither the main loop nor the remote overlay", key)
			}
		}
	}

	selected := startDialogOver(t, r, "Pick")
	requireDialogFocus(t, r.m)
	for _, key := range []string{"\x1b[B", "\r"} {
		if !route(key) {
			t.Fatalf("key %q reached the remote overlay while the dialog was shown", key)
		}
	}
	if got := requireResult(t, selected, "selector"); got.err != nil || got.value != "two" {
		t.Fatalf("selector = %q, %v; want two", got.value, got.err)
	}
	if focused := r.m.tuiInst.GetFocusedComponent(); any(focused) != overlay {
		t.Fatalf("focus after the dialog closed = %T, want the remote overlay", focused)
	}
	if route("x") {
		t.Fatal("key after the dialog closed reached the main loop, not the remote overlay")
	}
	overlay.Close("closed")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-remote:
			if !got.ok || got.value != "closed" {
				t.Fatalf("remote overlay = %v, %v", got.value, got.ok)
			}
			return
		case task := <-r.m.uiTaskCh:
			task()
		case <-timer.C:
			t.Fatal("remote overlay did not close")
		}
	}
}
