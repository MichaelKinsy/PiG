package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 0.87.1 runner.ts:320-351 ignores no-op mutations and never invokes factories.
func TestNoUIBridgeDoesNotRetainUIWork(t *testing.T) {
	bridge := NewUIBridge(func() { t.Error("headless invalidation") })
	bridge.SetNotifyFunc(func(string, string) { t.Error("headless notification") })
	bridge.OnStateChanged = func() { t.Error("headless state change") }
	for _, method := range []string{
		"ui.notify", "ui.setStatus", "ui.setWorkingIndicator", "ui.setWorkingMessage", "ui.setWorkingVisible",
		"ui.setHiddenThinkingLabel", "ui.setWidget", "ui.setFooter", "ui.setHeader", "ui.setTitle",
		"ui.setEditorComponent", "ui.pasteToEditor", "ui.setEditorText", "ui.setToolsExpanded",
		"ui.addAutocompleteProvider", "ui.onTerminalInput", "ui.offTerminalInput", "ui.custom",
	} {
		result, err := bridge.HandleCall("probe", &CallPayload{Method: method, Args: json.RawMessage(`{"key":"probe","text":"ignored","lines":["ignored"]}`)})
		if err != nil || result.Error != nil {
			t.Errorf("%s: %v %+v", method, err, result)
		}
	}
	bridge.HandleWidgetPush("probe", nil, &WidgetPushPayload{Key: "probe", Lines: []string{"ignored"}})
	bridge.reserveCustomOverlay("probe", nil, json.RawMessage(`{"key":"probe"}`))
	if len(bridge.AllWidgets()) != 0 || len(bridge.customOverlays) != 0 || len(bridge.terminalInputSubs) != 0 || len(bridge.pendingStatuses) != 0 || bridge.pendingHeaderSet || bridge.pendingFooterSet {
		t.Fatal("no-op UI retained work")
	}
	bridge.SetThemeFunc(func() any { return map[string]string{"accent": "active"} })
	theme, err := bridge.HandleCall("probe", &CallPayload{Method: "ui.theme"})
	if err != nil || string(theme.Result) != `{"theme":{"accent":"active"}}` {
		t.Fatalf("headless active theme = %+v, %v", theme, err)
	}
	ui := newFakeUIContext()
	bridge.SetUIContext(ui)
	if !bridge.Snapshot(nil, 0, false).HasUI {
		t.Fatal("bound UI is unavailable")
	}
	bridge.SetUIContext(extension.NoopUIContext)
	if bridge.Snapshot(nil, 0, false).HasUI {
		t.Fatal("unbound UI is available")
	}
}

func BenchmarkNoUIBridgeSnapshot(b *testing.B) {
	bridge := NewUIBridge(nil)
	for b.Loop() {
		_ = bridge.Snapshot(nil, 0, false)
	}
}
