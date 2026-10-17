package codingagent

import "testing"

// interactive-mode.ts switchTuiMode: `const onDebug = previousUi.onDebug; ... nextUi.onDebug = onDebug;` - the debug-key callback of the renderer being
// replaced, whatever set it, is the callback of its replacement in both directions.
func TestSwitchTuiModeCarriesTheDebugCallbackToTheNextRenderer(t *testing.T) {
	m := newSwitchTuiProbe(t)
	var calls []string
	for _, mode := range []string{"fullscreen", "regular"} {
		name := mode
		m.tuiInst.SetOnDebug(func() { calls = append(calls, name) })
		if !m.switchTuiMode(mode, false, false) {
			t.Fatalf("switch to %s refused", mode)
		}
		onDebug := m.tuiInst.OnDebug()
		if onDebug == nil {
			t.Fatalf("the %s renderer lost the debug callback", mode)
		}
		onDebug()
	}
	if len(calls) != 2 || calls[0] != "fullscreen" || calls[1] != "regular" {
		t.Fatalf("callbacks run = %v, want the previous renderer's callback each time", calls)
	}
}
