package subprocess

import (
	"slices"
	"testing"
)

// tuiUI is a UI that draws a TUI: it records what the extension asked of it, in order.
type tuiUI struct {
	*mockUIContext
	calls []string
}

func (u *tuiUI) InvalidateTUI() { u.calls = append(u.calls, "invalidate") }

func (u *tuiUI) RequestTUIRender(force bool) {
	if force {
		u.calls = append(u.calls, "requestRender force")
		return
	}
	u.calls = append(u.calls, "requestRender")
}

// D192: Pi's `tui.invalidate()` and `tui.requestRender(force)` reach the host's TUI in the order they were made.
func TestUIBridgeTUIHandleCallsReachTheUI(t *testing.T) {
	ui := &tuiUI{mockUIContext: &mockUIContext{}}
	bridge := newTestBridge(ui)
	for _, c := range [][2]string{
		{CallUIInvalidateTUI, `{}`},
		{CallUIRequestRender, `{"force":false}`},
		{CallUIRequestRender, `{"force":true}`},
		{CallUIRequestRender, `{}`},
	} {
		result, err := call(bridge, c[0], c[1])
		if err != nil || result.Error != nil {
			t.Fatalf("%s %s = %+v, %v", c[0], c[1], result, err)
		}
	}
	if want := []string{"invalidate", "requestRender", "requestRender force", "requestRender"}; !slices.Equal(ui.calls, want) {
		t.Fatalf("calls = %q, want %q", ui.calls, want)
	}
}

// A UI that draws no TUI (print, JSON and RPC mode, or the no-op UI before the TUI exists) ignores both calls, as the
// factories that would receive Pi's `tui` never run there.
func TestUIBridgeTUIHandleCallsAreIgnoredWithoutATUI(t *testing.T) {
	for name, bridge := range map[string]*UIBridge{
		"a UI that draws no TUI": newTestBridge(&mockUIContext{}),
		"the no-op UI":           NewUIBridge(func() {}),
	} {
		for _, method := range []string{CallUIInvalidateTUI, CallUIRequestRender} {
			result, err := call(bridge, method, `{"force":true}`)
			if err != nil || result == nil || result.Error != nil {
				t.Fatalf("%s: %s = %+v, %v; want success", name, method, result, err)
			}
		}
	}
}

func TestUIBridgeRequestRenderRejectsMalformedArguments(t *testing.T) {
	bridge := newTestBridge(&tuiUI{mockUIContext: &mockUIContext{}})
	if _, err := call(bridge, CallUIRequestRender, `{"force":"yes"}`); err == nil {
		t.Fatal("a force that is not a boolean was accepted")
	}
}
