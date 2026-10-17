package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 1.2.0 interactive-mode.ts:2966-3043 showExtensionCustom has no cross-call mutex: a second custom component opens while the
// first is shown. An overlay goes on the overlay stack and takes focus (tui.ts:729-742 showOverlay), and closing it pops the stack
// and focuses the topmost visible overlay or the focus it took over (tui.ts:829-841 hideOverlay). The TUI hands each key to the
// focused component only, so the component below receives nothing until the one above closes, and then it receives the next key.
// A subprocess extension's component reads the terminal through RunRemoteOverlay; it must follow the same focus.

// stackedRemote is one RunRemoteOverlay call: the component's handle, the keys its host received and the focus changes the host
// published to the extension.
type stackedRemote struct {
	label  string
	handle extension.RemoteOverlayHandle
	inputs chan string
	focus  chan bool
	result chan stackedRemoteResult
}

type stackedRemoteResult struct {
	value any
	ok    bool
}

// stackedRemoteHost is the bridge's side of the component: it receives its keys and its focus changes.
type stackedRemoteHost struct{ remote *stackedRemote }

func (h stackedRemoteHost) OnInput(data string)  { h.remote.inputs <- data }
func (h stackedRemoteHost) OnFocus(focused bool) { h.remote.focus <- focused }

// openRemote mounts a subprocess extension's custom component and pumps the owner loop until it is shown.
func (r *customRig) openRemote(t *testing.T, label string, overlay bool) *stackedRemote {
	t.Helper()
	remote := &stackedRemote{label: label, inputs: make(chan string, 8), focus: make(chan bool, 8), result: make(chan stackedRemoteResult, 1)}
	handles := make(chan extension.RemoteOverlayHandle, 1)
	r.workers.Go(func() {
		value, ok := r.u.RunRemoteOverlay(extension.RemoteOverlayOptions{Overlay: overlay}, stackedRemoteHost{remote}, func(handle extension.RemoteOverlayHandle) {
			handle.UpdateLines([]string{label})
			handles <- handle
		})
		remote.result <- stackedRemoteResult{value, ok}
	})
	r.pump(t, nil, func() bool {
		select {
		case remote.handle = <-handles:
			return true
		default:
			return false
		}
	})
	return remote
}

// requireFocusEvents pumps the owner loop until the host published want, in order, and no other focus change.
func (s *stackedRemote) requireFocusEvents(t *testing.T, r *customRig, want ...bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for index := 0; index < len(want); {
		select {
		case got := <-s.focus:
			if got != want[index] {
				t.Fatalf("focus change %d published to %s = %t, want %t", index, s.label, got, want[index])
			}
			index++
		case task := <-r.m.uiTaskCh:
			task()
		case <-timer.C:
			t.Fatalf("focus changes published to %s: %d of %v", s.label, index, want)
		}
	}
	select {
	case got := <-s.focus:
		t.Fatalf("%s got an extra focus change %t", s.label, got)
	default:
	}
}

// close ends the remote component with value and pumps the owner loop until RunRemoteOverlay returned it.
func (s *stackedRemote) close(t *testing.T, r *customRig, value string) {
	t.Helper()
	s.handle.Close(value)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-s.result:
			if !got.ok || got.value != value {
				t.Fatalf("remote component %s = %v, %v; want %q", s.label, got.value, got.ok, value)
			}
			return
		case task := <-r.m.uiTaskCh:
			task()
		case <-timer.C:
			t.Fatalf("remote component %s did not close", s.label)
		}
	}
}

// reading reports whether the remote component's loop holds a terminal input lease.
func (s *stackedRemote) reading() bool {
	route, _ := s.handle.(*customOverlay).inputRoute()
	return route != nil
}

// routeKey sends one key through the input pump and reports where it went: "main" when the main loop took it and dispatched it to the
// focused component, or the label of the remote component whose host received it.
func (r *customRig) routeKey(t *testing.T, key string, remotes ...*stackedRemote) string {
	t.Helper()
	ctx := r.m.runCtx
	readCh := make(chan inputChunk, 1)
	r.workers.Go(func() { r.m.routeInputChunk(ctx, []byte(key), readCh) })
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		for _, remote := range remotes {
			select {
			case got := <-remote.inputs:
				if got != key {
					t.Fatalf("remote component %s input = %q, want %q", remote.label, got, key)
				}
				return remote.label
			default:
			}
		}
		select {
		case chunk := <-readCh:
			if err := r.m.dispatchKey(ctx, string(chunk.data)); err != nil {
				t.Fatal(err)
			}
			return "main"
		case task := <-r.m.uiTaskCh:
			task()
		case <-timer.C:
			t.Fatalf("key %q reached neither the main loop nor a remote component", key)
		case <-time.After(time.Millisecond):
		}
	}
}

func requireFocus(t *testing.T, m *InteractiveMode, want tui.Component, what string) {
	t.Helper()
	if focused := m.tuiInst.GetFocusedComponent(); focused != want {
		t.Fatalf("focus %s = %T, want %T", what, focused, want)
	}
}

// Two subprocess overlays: the second opens over the first, alone reads the terminal, and on close hands it back to the first. The
// first learns it lost focus and got it back, as Pi's setFocus clears and sets its focused flag.
func TestRemoteCustomOverlaysStackAndHandFocusBack(t *testing.T) {
	r := newCustomRig(t)
	r.m.tuiInst.SetFocus(r.m.editor)
	back := r.openRemote(t, "BACK", true)
	front := r.openRemote(t, "FRONT", true)
	requireFocus(t, r.m, front.handle.(*customOverlay), "with two overlays shown")
	if back.reading() || !front.reading() {
		t.Fatalf("terminal readers: back=%t front=%t; want only the focused front overlay", back.reading(), front.reading())
	}
	back.requireFocusEvents(t, r, false)
	front.requireFocusEvents(t, r)
	if got := r.routeKey(t, "a", back, front); got != "FRONT" {
		t.Fatalf("key with two overlays shown went to %s, want FRONT", got)
	}
	front.close(t, r, "front")
	requireFocus(t, r.m, back.handle.(*customOverlay), "after the front overlay closed")
	back.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "b", back); got != "BACK" {
		t.Fatalf("key after the front overlay closed went to %s, want BACK", got)
	}
	back.close(t, r, "back")
	requireFocus(t, r.m, r.m.editor, "after both overlays closed")
}

// A subprocess overlay over a subprocess editor-slot component: the overlay takes focus from it, and closing the overlay returns focus
// to the component it took it from (tui.ts:838 entry.preFocus).
func TestRemoteCustomOverlayStacksOverRemoteEditorSlotComponent(t *testing.T) {
	r := newCustomRig(t)
	slot := r.openRemote(t, "SLOT", false)
	requireFocus(t, r.m, slot.handle.(*customOverlay), "with the editor-slot component shown")
	front := r.openRemote(t, "FRONT", true)
	if slot.reading() || !front.reading() {
		t.Fatalf("terminal readers: slot=%t front=%t; want only the focused overlay", slot.reading(), front.reading())
	}
	slot.requireFocusEvents(t, r, false)
	if got := r.routeKey(t, "a", slot, front); got != "FRONT" {
		t.Fatalf("key with the overlay shown went to %s, want FRONT", got)
	}
	front.close(t, r, "front")
	requireFocus(t, r.m, slot.handle.(*customOverlay), "after the overlay closed")
	slot.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "b", slot); got != "SLOT" {
		t.Fatalf("key after the overlay closed went to %s, want SLOT", got)
	}
	slot.close(t, r, "slot")
	requireFocus(t, r.m, r.m.editor, "after the editor-slot component closed")
}

// A host-process overlay over a subprocess overlay: the TUI dispatches the keys to the focused host component, so the subprocess
// component stops reading the terminal until the host component closes.
func TestHostCustomOverlayStacksOverRemoteOverlay(t *testing.T) {
	r := newCustomRig(t)
	back := r.openRemote(t, "BACK", true)
	component := &customProbe{label: "HOST"}
	var done func(any)
	out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return component, nil
	}, &extension.CustomOptions{Overlay: true})
	r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component })
	if back.reading() {
		t.Fatal("the subprocess overlay still reads the terminal under the focused host overlay")
	}
	back.requireFocusEvents(t, r, false)
	if got := r.routeKey(t, "a", back); got != "main" {
		t.Fatalf("key with the host overlay shown went to %s, want the main loop", got)
	}
	if got := component.received(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("host overlay input = %q, want a", got)
	}
	go done("host")
	if got := r.finish(t, out); got.err != nil || got.value != "host" {
		t.Fatalf("custom = %v, %v", got.value, got.err)
	}
	requireFocus(t, r.m, back.handle.(*customOverlay), "after the host overlay closed")
	back.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "b", back); got != "BACK" {
		t.Fatalf("key after the host overlay closed went to %s, want BACK", got)
	}
	if got := component.received(); len(got) != 1 {
		t.Fatalf("the closed host overlay received %q", got)
	}
	back.close(t, r, "back")
}

// A subprocess overlay over a host-process overlay: the subprocess overlay reads the keys, and the host overlay gets the next key once
// it closes.
func TestRemoteCustomOverlayStacksOverHostOverlay(t *testing.T) {
	r := newCustomRig(t)
	component := &customProbe{label: "HOST"}
	var done func(any)
	out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return component, nil
	}, &extension.CustomOptions{Overlay: true})
	r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component })
	front := r.openRemote(t, "FRONT", true)
	if got := r.routeKey(t, "a", front); got != "FRONT" {
		t.Fatalf("key with the subprocess overlay shown went to %s, want FRONT", got)
	}
	if got := component.received(); len(got) != 0 {
		t.Fatalf("the host overlay under the subprocess overlay received %q", got)
	}
	front.close(t, r, "front")
	requireFocus(t, r.m, component, "after the subprocess overlay closed")
	if got := r.routeKey(t, "b", front); got != "main" {
		t.Fatalf("key after the subprocess overlay closed went to %s, want the main loop", got)
	}
	if got := component.received(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("host overlay input = %q, want b", got)
	}
	go done("host")
	if got := r.finish(t, out); got.err != nil || got.value != "host" {
		t.Fatalf("custom = %v, %v", got.value, got.err)
	}
}

// Two host-process overlays: the second takes the keys and on close focus returns to the first.
func TestHostCustomOverlaysStackAndHandFocusBack(t *testing.T) {
	r := newCustomRig(t)
	open := func(label string) (*customProbe, *func(any), <-chan customOutcome) {
		component := &customProbe{label: label}
		done := new(func(any))
		out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
			*done = d
			return component, nil
		}, &extension.CustomOptions{Overlay: true})
		r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component })
		return component, done, out
	}
	back, backDone, backOut := open("BACK")
	front, frontDone, frontOut := open("FRONT")
	if err := r.m.dispatchKey(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	if got := front.received(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("front overlay input = %q, want a", got)
	}
	go (*frontDone)("front")
	if got := r.finish(t, frontOut); got.err != nil || got.value != "front" {
		t.Fatalf("front custom = %v, %v", got.value, got.err)
	}
	requireFocus(t, r.m, back, "after the front overlay closed")
	if err := r.m.dispatchKey(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}
	if got := back.received(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("back overlay input = %q, want b", got)
	}
	go (*backDone)("back")
	if got := r.finish(t, backOut); got.err != nil || got.value != "back" {
		t.Fatalf("back custom = %v, %v", got.value, got.err)
	}
}

// Pi 1.3.0 interactive-mode.ts:2999 (#10999, test interactive-mode-status.test.ts "closing a lower $lowerKind overlay custom UI removes
// its own overlay, not the topmost one"): done() of an overlay component hides the overlay through its own handle. Closing the lower
// of two overlays removes the lower one, leaves the upper one shown with the focus and the keys, and closing the upper one then
// leaves no overlay and the editor focused. The lower overlay is capturing or non-capturing.
func TestHostCustomOverlayCloseOutOfOrderRemovesItsOwnOverlay(t *testing.T) {
	for _, lower := range []struct {
		kind         string
		nonCapturing bool
	}{{"capturing", false}, {"non-capturing", true}} {
		t.Run(lower.kind, func(t *testing.T) {
			r := newCustomRig(t)
			r.m.tuiInst.SetFocus(r.m.editor)
			open := func(label string, options extension.OverlayOptionsSource) (*customProbe, *func(any), <-chan customOutcome) {
				component := &customProbe{label: label}
				done := new(func(any))
				out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
					*done = d
					return component, nil
				}, &extension.CustomOptions{Overlay: true, OverlayOptions: options})
				r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component })
				return component, done, out
			}
			var options extension.OverlayOptionsSource
			if lower.nonCapturing {
				options = extension.OverlayOptionsValue{Options: tui.OverlaySpec{NonCapturing: true}.Options()}
			}
			var lowerComponent *customProbe
			var closeLower *func(any)
			var lowerOut <-chan customOutcome
			if lower.nonCapturing {
				// A non-capturing overlay does not take the focus; the pump waits for the component to mount instead.
				lowerComponent = &customProbe{label: "LOWER"}
				closeLower = new(func(any))
				lowerOut = r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
					*closeLower = d
					return lowerComponent, nil
				}, &extension.CustomOptions{Overlay: true, OverlayOptions: options})
				r.pump(t, lowerOut, func() bool { return r.m.tuiInst.HasOverlay() })
			} else {
				lowerComponent, closeLower, lowerOut = open("LOWER", options)
			}
			upper, closeUpper, upperOut := open("UPPER", nil)
			requireFocus(t, r.m, upper, "with both overlays shown")
			go (*closeLower)("lower closed")
			if got := r.finish(t, lowerOut); got.err != nil || got.value != "lower closed" {
				t.Fatalf("lower custom = %v, %v", got.value, got.err)
			}
			if !r.m.tuiInst.HasOverlay() {
				t.Fatal("closing the lower overlay removed the upper one too")
			}
			requireFocus(t, r.m, upper, "after the lower overlay closed")
			if err := r.m.dispatchKey(t.Context(), "x"); err != nil {
				t.Fatal(err)
			}
			if got := upper.received(); len(got) != 1 || got[0] != "x" {
				t.Fatalf("upper overlay input = %q, want x", got)
			}
			if got := lowerComponent.received(); len(got) != 0 {
				t.Fatalf("the closed lower overlay received %q", got)
			}
			go (*closeUpper)("upper closed")
			if got := r.finish(t, upperOut); got.err != nil || got.value != "upper closed" {
				t.Fatalf("upper custom = %v, %v", got.value, got.err)
			}
			if r.m.tuiInst.HasOverlay() {
				t.Fatal("an overlay is left after both closed")
			}
			requireFocus(t, r.m, r.m.editor, "after both overlays closed")
		})
	}
}

// The same for subprocess overlays (RunRemoteOverlay): the back component's close removes its own overlay and the front one keeps the
// focus and the keys.
func TestRemoteCustomOverlayCloseOutOfOrderRemovesItsOwnOverlay(t *testing.T) {
	r := newCustomRig(t)
	r.m.tuiInst.SetFocus(r.m.editor)
	back := r.openRemote(t, "BACK", true)
	front := r.openRemote(t, "FRONT", true)
	back.close(t, r, "back")
	if !r.m.tuiInst.HasOverlay() {
		t.Fatal("closing the back overlay removed the front one too")
	}
	requireFocus(t, r.m, front.handle.(*customOverlay), "after the back overlay closed first")
	if got := r.routeKey(t, "x", front); got != "FRONT" {
		t.Fatalf("key after the back overlay closed went to %s, want FRONT", got)
	}
	front.close(t, r, "front")
	if r.m.tuiInst.HasOverlay() {
		t.Fatal("an overlay is left after both closed")
	}
	requireFocus(t, r.m, r.m.editor, "after both overlays closed")
}

// onWorker runs fn off the owner loop and pumps the owner loop until it returns.
func (r *customRig) onWorker(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	r.workers.Go(func() {
		defer close(done)
		fn()
	})
	r.pump(t, nil, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
}

// showRaw shows a subprocess component as a raw overlay, as a factory's tui.showOverlay does, and returns it with the channel that
// closes once it ended.
func (r *customRig) showRaw(t *testing.T, label string, nonCapturing bool) (*stackedRemote, <-chan struct{}) {
	t.Helper()
	remote := &stackedRemote{label: label, inputs: make(chan string, 8), focus: make(chan bool, 8)}
	var ended <-chan struct{}
	r.onWorker(t, func() {
		handle, done, ok := r.u.ShowRemoteOverlay(extension.RemoteOverlayOptions{Layout: &extension.OverlayLayout{NonCapturing: nonCapturing}}, stackedRemoteHost{remote})
		if !ok {
			return
		}
		handle.UpdateLines([]string{label})
		remote.handle, ended = handle, done
	})
	if remote.handle == nil {
		t.Fatalf("raw overlay %s was not shown", label)
	}
	return remote, ended
}

// control applies one OverlayHandle operation to a remote component and returns the state the host answered.
func (r *customRig) control(t *testing.T, remote *stackedRemote, action string) extension.RemoteOverlayState {
	t.Helper()
	var state extension.RemoteOverlayState
	var err error
	r.onWorker(t, func() { state, err = remote.handle.(*customOverlay).Control(r.m.runCtx, action, false, nil) })
	if err != nil {
		t.Fatalf("%s %s: %v", remote.label, action, err)
	}
	return state
}

// awaitEnded pumps the owner loop until a raw overlay ended.
func (r *customRig) awaitEnded(t *testing.T, label string, ended <-chan struct{}) {
	t.Helper()
	r.pump(t, nil, func() bool {
		select {
		case <-ended:
			return true
		default:
			return false
		}
	})
}

// Pi 1.2.0 overlay-qa-tests.ts FocusDemoController shows non-capturing panels with tui.showOverlay inside its ui.custom factory and
// focuses one before the factory returns; the controller overlay then mounts over them and takes focus (tui.ts:729-742). A panel's
// handle.focus() gives it the keys (tui.ts:783-788); its hide removes exactly that panel and focuses the topmost visible capturing
// overlay (tui.ts:747-761); the controller's close pops the topmost overlay and focuses its preFocus (tui.ts:829-841).
func TestRemoteRawOverlaysFollowPiShowOverlay(t *testing.T) {
	r := newCustomRig(t)
	r.m.tuiInst.SetFocus(r.m.editor)
	alpha, alphaEnded := r.showRaw(t, "ALPHA", true)
	beta, betaEnded := r.showRaw(t, "BETA", true)
	requireFocus(t, r.m, r.m.editor, "after two non-capturing overlays were shown")
	if alpha.reading() || beta.reading() {
		t.Fatalf("terminal readers: alpha=%t beta=%t; a non-capturing overlay takes no focus", alpha.reading(), beta.reading())
	}
	if state := r.control(t, alpha, "focus"); !state.Focused || state.FocusOwner != any(alpha.handle) {
		t.Fatalf("alpha focus answered %+v, want it focused and named the focus owner", state)
	}
	requireFocus(t, r.m, alpha.handle.(*customOverlay), "after alpha's handle.focus()")
	alpha.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "a", alpha, beta); got != "ALPHA" {
		t.Fatalf("key with alpha focused went to %s, want ALPHA", got)
	}
	owner := r.openRemote(t, "OWNER", true)
	requireFocus(t, r.m, owner.handle.(*customOverlay), "after the controller overlay mounted")
	alpha.requireFocusEvents(t, r, false)
	if got := r.routeKey(t, "b", alpha, beta, owner); got != "OWNER" {
		t.Fatalf("key with the controller shown went to %s, want OWNER", got)
	}
	r.control(t, beta, "focus")
	requireFocus(t, r.m, beta.handle.(*customOverlay), "after beta's handle.focus()")
	beta.requireFocusEvents(t, r, true)
	owner.requireFocusEvents(t, r, false)
	if got := r.routeKey(t, "c", alpha, beta, owner); got != "BETA" {
		t.Fatalf("key with beta focused went to %s, want BETA", got)
	}
	if state := r.control(t, beta, "hide"); state.Visible || state.FocusOwner != any(owner.handle) {
		t.Fatalf("beta hide answered %+v, want it gone and the controller focused", state)
	}
	r.awaitEnded(t, "BETA", betaEnded)
	requireFocus(t, r.m, owner.handle.(*customOverlay), "after beta's hide")
	owner.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "d", alpha, owner); got != "OWNER" {
		t.Fatalf("key after beta's hide went to %s, want OWNER", got)
	}
	// The controller's close pops itself, not alpha, and focus returns to its preFocus, alpha.
	owner.close(t, r, "owner")
	requireFocus(t, r.m, alpha.handle.(*customOverlay), "after the controller closed")
	alpha.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "e", alpha); got != "ALPHA" {
		t.Fatalf("key after the controller closed went to %s, want ALPHA", got)
	}
	r.control(t, alpha, "hide")
	r.awaitEnded(t, "ALPHA", alphaEnded)
	requireFocus(t, r.m, r.m.editor, "after alpha's hide")
}

// A raw overlay the bridge closes because the extension's connection ended leaves the stack by itself: an overlay shown over it
// stays shown and focused (Pi's handle.hide, tui.ts:747-761, not hideOverlay's pop).
func TestRemoteRawOverlayCloseRemovesOnlyItsEntry(t *testing.T) {
	r := newCustomRig(t)
	r.m.tuiInst.SetFocus(r.m.editor)
	child, ended := r.showRaw(t, "CHILD", false)
	requireFocus(t, r.m, child.handle.(*customOverlay), "after a capturing raw overlay was shown")
	child.requireFocusEvents(t, r)
	owner := r.openRemote(t, "OWNER", true)
	child.requireFocusEvents(t, r, false)
	child.handle.Close(nil)
	r.awaitEnded(t, "CHILD", ended)
	requireFocus(t, r.m, owner.handle.(*customOverlay), "after the raw overlay below it ended")
	if got := r.routeKey(t, "a", child, owner); got != "OWNER" {
		t.Fatalf("key after the raw overlay ended went to %s, want OWNER", got)
	}
	owner.close(t, r, "owner")
	requireFocus(t, r.m, r.m.editor, "after the controller closed")
}
