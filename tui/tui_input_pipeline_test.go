package tui

import (
	"bytes"
	"strings"
	"testing"
)

// Pi tui.ts handleTerminalInput (1044-1160), addInputListener/removeInputListener (932-941), onTerminalColorSchemeChange and setTerminalColorSchemeNotifications (943-958), start/stop (916, 970).

type recordingInput struct {
	Text
	got []string
}

func (r *recordingInput) HandleInput(data string) { r.got = append(r.got, data) }

func listener(log *[]string, name string, fn func(string) *TuiInputResult) *TuiInputListener {
	l := TuiInputListener(func(data string) *TuiInputResult {
		*log = append(*log, name+":"+data)
		if fn == nil {
			return nil
		}
		return fn(data)
	})
	return &l
}

func TestInputListenersRunInRegistrationOrderAndTransformData(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	var log []string
	ui.AddInputListener(listener(&log, "a", func(string) *TuiInputResult { return &TuiInputResult{Data: new("B")} }))
	ui.AddInputListener(listener(&log, "b", nil))
	ui.HandleTerminalInput("x")
	if want := []string{"a:x", "b:B"}; !equalStrings(log, want) {
		t.Fatalf("listener calls = %v, want %v", log, want)
	}
	if !equalStrings(focus.got, []string{"B"}) {
		t.Fatalf("focused component received %v, want the transformed [B]", focus.got)
	}
}

func TestInputListenerConsumeStopsLaterListenersAndTheFocusedComponent(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	var log []string
	ui.AddInputListener(listener(&log, "a", func(string) *TuiInputResult { return &TuiInputResult{Consume: true} }))
	ui.AddInputListener(listener(&log, "b", nil))
	ui.HandleTerminalInput("x")
	if !equalStrings(log, []string{"a:x"}) || len(focus.got) != 0 {
		t.Fatalf("log = %v, focus = %v; a consumed input must reach no one else", log, focus.got)
	}
}

func TestInputListenerEmptyReplacementDropsTheInput(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	var log []string
	ui.AddInputListener(listener(&log, "a", func(string) *TuiInputResult { return &TuiInputResult{Data: new("")} }))
	ui.HandleTerminalInput("x")
	if len(focus.got) != 0 {
		t.Fatalf("focused component received %v after an empty replacement", focus.got)
	}
}

func TestInputListenersCanBeRemovedByTheirHandleOrByIdentity(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	var log []string
	a := listener(&log, "a", nil)
	b := listener(&log, "b", nil)
	remove := ui.AddInputListener(a)
	ui.AddInputListener(b)
	remove()
	ui.HandleTerminalInput("1")
	ui.RemoveInputListener(b)
	ui.HandleTerminalInput("2")
	if !equalStrings(log, []string{"b:1"}) {
		t.Fatalf("listener calls = %v, want [b:1]", log)
	}
}

func TestTerminalColorSchemeReportIsConsumedBeforeInputListeners(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	var log []string
	ui.AddInputListener(listener(&log, "a", nil))
	var schemes []TerminalColorScheme
	remove := ui.OnTerminalColorSchemeChange(func(s TerminalColorScheme) { schemes = append(schemes, s) })
	ui.HandleTerminalInput("\x1b[?997;2n")
	ui.HandleTerminalInput("\x1b[?997;1n")
	remove()
	ui.HandleTerminalInput("\x1b[?997;2n")
	if !equalStrings([]string{string(schemes[0]), string(schemes[1])}, []string{"light", "dark"}) || len(schemes) != 2 {
		t.Fatalf("schemes = %v, want [light dark]", schemes)
	}
	if len(log) != 0 || len(focus.got) != 0 {
		t.Fatalf("a scheme report reached listeners %v or focus %v", log, focus.got)
	}
}

func TestTerminalColorSchemeNotificationsFollowTheRendererLifecycle(t *testing.T) {
	var out bytes.Buffer
	ui := newManualRenderTUI(&out, 40, 5)
	ui.SetTerminalColorSchemeNotifications(true)
	ui.SetTerminalColorSchemeNotifications(true)
	if got := out.String(); got != "\x1b[?2031h" {
		t.Fatalf("enable wrote %q, want one \\x1b[?2031h", got)
	}
	out.Reset()
	ui.Stop()
	if got := out.String(); len(got) < 8 || got[:8] != "\x1b[?2031l" {
		t.Fatalf("Stop wrote %q, want it to start with \\x1b[?2031l", got)
	}
	out.Reset()
	ui.SetTerminalColorSchemeNotifications(false)
	if out.Len() != 0 {
		t.Fatalf("disabling while stopped wrote %q", out.String())
	}
	ui.SetTerminalColorSchemeNotifications(true)
	if out.Len() != 0 {
		t.Fatalf("enabling while stopped wrote %q", out.String())
	}
	ui.Start()
	if !bytes.Contains(out.Bytes(), []byte("\x1b[?2031h")) {
		t.Fatalf("Start wrote %q, want \\x1b[?2031h for the enabled notifications", out.String())
	}
}

func TestHandleTerminalInputHidesPendingColorRepliesAndCellSizeFromTheFocus(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	ui.HandleTerminalInput("\x1b[6;18;9t")
	ui.HandleTerminalInput("k")
	if !equalStrings(focus.got, []string{"k"}) {
		t.Fatalf("focused component received %v, want only [k]", focus.got)
	}
}

func TestGetFocusedComponentReportsTheFocusTarget(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	if ui.GetFocusedComponent() != nil {
		t.Fatal("a new TUI has a focused component")
	}
	focus := &recordingInput{}
	ui.SetFocus(focus)
	if ui.GetFocusedComponent() != Component(focus) {
		t.Fatal("GetFocusedComponent did not return the component given to SetFocus")
	}
}

func TestAltScreenRegistersItsViewportInputAsAnInputListener(t *testing.T) {
	ui := NewTuiAltScreenWithOutput(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
	if _, consumed := ui.RunInputListeners(altFocusIn); !consumed {
		t.Fatal("a focus-in report reached past the viewport input listener")
	}
	if data, consumed := ui.RunInputListeners("zzz"); consumed || data != "zzz" {
		t.Fatalf("RunInputListeners(zzz) = %q, %v, want zzz, false", data, consumed)
	}
}

// Pi tui.ts start writes \x1b[?2031h after beforeTerminalStart enters the alternate screen, and stop writes \x1b[?2031l before
// beforeTerminalStop leaves it, so the external editor, suspend and renderer swaps keep the reports in fullscreen mode too.
func TestAltScreenColorSchemeNotificationsFollowTheRendererLifecycle(t *testing.T) {
	var out bytes.Buffer
	ui := NewTuiAltScreenWithOutput(&out, 40, 10, TuiAltScreenOptions{})
	ui.Start()
	out.Reset()
	ui.SetTerminalColorSchemeNotifications(true)
	if got := out.String(); got != "\x1b[?2031h" {
		t.Fatalf("enable wrote %q, want one \\x1b[?2031h", got)
	}
	out.Reset()
	ui.Stop()
	stop := out.String()
	disable, exit := strings.Index(stop, "\x1b[?2031l"), strings.Index(stop, altExitAltScreen)
	if disable < 0 || exit < 0 || disable > exit {
		t.Fatalf("Stop wrote %q, want \\x1b[?2031l before leaving the alternate screen", stop)
	}
	out.Reset()
	ui.Start()
	start := out.String()
	enter, enable := strings.Index(start, altEnterAltScreen), strings.Index(start, "\x1b[?2031h")
	if enter < 0 || enable < 0 || enable < enter {
		t.Fatalf("Start wrote %q, want \\x1b[?2031h after entering the alternate screen", start)
	}
	ui.Stop()
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

// releaseAware is a focused component that opts in to Kitty key releases (tui.ts Component.wantsKeyRelease).
type releaseAware struct{ recordingInput }

func (*releaseAware) WantsKeyRelease() bool { return true }

const kittyKeyRelease = "\x1b[97;1:3u"

// Pi tui.ts:1075-1078: the global debug key runs onDebug and goes no further; with no callback the key is ordinary input.
func TestHandleTerminalInputRoutesTheDebugKeyOnlyWhenACallbackIsSet(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	const debugKey = "\x1b[100;6u"
	ui.HandleTerminalInput(debugKey)
	if !equalStrings(focus.got, []string{debugKey}) {
		t.Fatalf("without a callback the focus received %v, want the debug key", focus.got)
	}
	calls := 0
	ui.SetOnDebug(func() { calls++ })
	ui.HandleTerminalInput(debugKey)
	if calls != 1 || len(focus.got) != 1 {
		t.Fatalf("callback ran %d times and the focus received %v; the debug key must stop at the callback", calls, focus.got)
	}
}

// tui.ts:1070-1078: the input listeners see the data before the debug key is matched, so a listener that replaces it hides it from the callback.
func TestHandleTerminalInputMatchesTheDebugKeyAfterTheInputListeners(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	calls := 0
	ui.SetOnDebug(func() { calls++ })
	var log []string
	ui.AddInputListener(listener(&log, "swap", func(string) *TuiInputResult { return &TuiInputResult{Data: new("z")} }))
	ui.HandleTerminalInput("\x1b[100;6u")
	if calls != 0 || !equalStrings(focus.got, []string{"z"}) {
		t.Fatalf("callback ran %d times and the focus received %v, want 0 calls and [z]", calls, focus.got)
	}
}

// tui.ts:1108-1115: a key release reaches the focused component only when it sets wantsKeyRelease.
func TestHandleTerminalInputDropsKeyReleasesUnlessTheFocusAsksForThem(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	plain := &recordingInput{}
	ui.SetFocus(plain)
	ui.HandleTerminalInput(kittyKeyRelease)
	if len(plain.got) != 0 {
		t.Fatalf("a component that did not ask for releases received %v", plain.got)
	}
	aware := &releaseAware{}
	ui.SetFocus(aware)
	ui.HandleTerminalInput(kittyKeyRelease)
	if !equalStrings(aware.got, []string{kittyKeyRelease}) {
		t.Fatalf("a component that asked for releases received %v", aware.got)
	}
}

// tui.ts:1066-1068: input listeners run before the release filter, so a listener sees every release.
func TestHandleTerminalInputShowsKeyReleasesToTheInputListeners(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	ui.SetFocus(&recordingInput{})
	var log []string
	ui.AddInputListener(listener(&log, "a", nil))
	ui.HandleTerminalInput(kittyKeyRelease)
	if !equalStrings(log, []string{"a:" + kittyKeyRelease}) {
		t.Fatalf("listener calls = %v, want the release", log)
	}
}

// tui.ts:1085-1105: a visible overlay owns input over the focus set before it opened, and a hidden overlay gives it back.
func TestHandleTerminalInputFollowsOverlayVisibility(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	base := &recordingInput{}
	ui.SetFocus(base)
	overlay := &recordingInput{}
	handle := ui.ShowOverlay(overlay, OverlayOptions{})
	ui.HandleTerminalInput("a")
	if !equalStrings(overlay.got, []string{"a"}) || len(base.got) != 0 {
		t.Fatalf("overlay received %v and base %v, want the overlay to own the key", overlay.got, base.got)
	}
	handle.setHidden(true)
	ui.HandleTerminalInput("b")
	if !equalStrings(base.got, []string{"b"}) || !equalStrings(overlay.got, []string{"a"}) {
		t.Fatalf("after hiding, base received %v and overlay %v, want the key back on the base", base.got, overlay.got)
	}
}

// tui.ts:1108-1118: keyboard input is latency-sensitive, so a delivered key requests an immediate render and a dropped one does not.
func TestHandleTerminalInputRequestsAnImmediateRenderOnlyForDeliveredKeys(t *testing.T) {
	var out bytes.Buffer
	ui := newManualRenderTUI(&out, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	pending := func() bool {
		ui.mu.Lock()
		defer ui.mu.Unlock()
		return ui.immediateRenderRequested
	}
	ui.HandleTerminalInput(kittyKeyRelease)
	if pending() {
		t.Fatal("a dropped key release requested a render")
	}
	ui.HandleTerminalInput("k")
	if !pending() {
		t.Fatal("a delivered key did not request an immediate render")
	}
}

// tui.ts:1085-1095: an overlay that stops being visible (the visible callback, e.g. after a resize) is checked when input arrives, and the input goes to the focus it gives back.
func TestHandleTerminalInputRedirectsFromAnOverlayThatBecameInvisible(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	base := &recordingInput{}
	ui.SetFocus(base)
	overlay := &recordingInput{}
	shown := true
	ui.ShowOverlay(overlay, OverlayOptions{visible: func(int, int) bool { return shown }})
	ui.HandleTerminalInput("a")
	shown = false
	ui.HandleTerminalInput("b")
	if !equalStrings(overlay.got, []string{"a"}) || !equalStrings(base.got, []string{"b"}) {
		t.Fatalf("overlay received %v and base %v, want [a] and [b]", overlay.got, base.got)
	}
}

// tui.ts:1076-1117: after the cell-size reply the global debug key (Shift+Ctrl+D) runs onDebug and goes no further, whatever has focus; any other
// input goes to the focused component, and a delivery requests an immediate render.
func TestHandleTerminalInputRunsTheDebugKeyBeforeTheFocusedComponent(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	debugCalls := 0
	const shiftCtrlD = "\x1b[100;6u"
	ui.HandleTerminalInput(shiftCtrlD)
	if len(focus.got) != 1 {
		t.Fatalf("without onDebug the key reaches the focused component: %q", focus.got)
	}
	ui.SetOnDebug(func() { debugCalls++ })
	ui.HandleTerminalInput(shiftCtrlD)
	ui.HandleTerminalInput("x")
	if debugCalls != 1 || !equalStrings(focus.got, []string{shiftCtrlD, "x"}) {
		t.Fatalf("debug calls %d, focused input %q; want the debug key consumed once and x delivered", debugCalls, focus.got)
	}
}

// tui.ts:1110-1117: Kitty key releases are not delivered unless the focused component asks (wantsKeyRelease), and the input listeners run first.
func TestHandleTerminalInputFiltersKeyReleasesUnlessTheFocusedComponentWantsThem(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	plain := &recordingInput{}
	ui.SetFocus(plain)
	const release = "\x1b[97;1:3u"
	ui.HandleTerminalInput(release)
	ui.HandleTerminalInput("a")
	if !equalStrings(plain.got, []string{"a"}) {
		t.Fatalf("plain component got %q, want the release dropped", plain.got)
	}
	wanting := &releaseInput{}
	ui.SetFocus(wanting)
	ui.HandleTerminalInput(release)
	if !equalStrings(wanting.got, []string{release}) {
		t.Fatalf("a component with wantsKeyRelease got %q", wanting.got)
	}
}

type releaseInput struct{ recordingInput }

func (*releaseInput) WantsKeyRelease() bool { return true }

// tui.ts:1110-1117: a key delivered to the focused component requests an immediate render (keyboard input avoids the throttled timer path); a key
// release the component does not ask for is dropped without one. DispatchFocusedInput is the stage the interactive driver calls.
func TestDispatchFocusedInputRequestsAnImmediateRenderOnlyForADeliveredKey(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &recordingInput{}
	ui.SetFocus(focus)
	renders := 0
	ui.SetRenderDispatcher(func(func()) { renders++ })
	ui.DispatchFocusedInput("\x1b[97;1:3u")
	if renders != 0 || len(focus.got) != 0 {
		t.Fatalf("a dropped key release: %d renders, delivered %q; want none", renders, focus.got)
	}
	ui.DispatchFocusedInput("a")
	if renders != 1 || !equalStrings(focus.got, []string{"a"}) {
		t.Fatalf("a delivered key: %d renders, delivered %q; want one immediate render", renders, focus.got)
	}
}
