package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi puts widgets above the editor unless the extension selects belowEditor.
func TestFullscreenWidgetPlacementThroughBridge(t *testing.T) {
	m := newFullscreenProbe(t)
	bridge := subprocess.NewUIBridge(func() {})
	m.opts.SubprocessUIBridge = bridge
	m.attachSubprocess()
	defer m.detachSubprocess()
	for _, args := range []string{
		`{"key":"above","content":["above-widget"]}`,
		`{"key":"below","content":["below-widget"],"options":{"placement":"belowEditor"}}`,
	} {
		if _, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: "ui.setWidget", Args: json.RawMessage(args)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"fullscreen", "regular", "fullscreen"} {
		if !m.switchTuiMode(mode, false, true) {
			t.Fatal("mode switch failed")
		}
		lines := m.tuiInst.RenderSnapshot(80)
		rows := strings.Join(lines, "\n")
		above, editor, below := strings.Index(rows, "above-widget"), strings.Index(rows, widthx.CursorMarker), strings.Index(rows, "below-widget")
		if above < 0 || editor < 0 || below < 0 || above >= editor || editor >= below {
			t.Fatalf("%s widget placement incorrect: %q", mode, lines)
		}
	}
	// A replacement without options moves the widget back above the editor.
	if _, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: "ui.setWidget", Args: json.RawMessage(`{"key":"below","content":["moved-widget"]}`)}); err != nil {
		t.Fatal(err)
	}
	if got := m.widgetContainerBelow.Render(80); len(got) != 0 {
		t.Fatalf("old below slot retained widget: %q", got)
	}
	if !strings.Contains(strings.Join(m.widgetContainer.Render(80), "\n"), "moved-widget") {
		t.Fatal("widget did not move above editor")
	}
	bridge.ClearExtension("probe")
	if got := m.widgetContainerBelow.Render(80); len(got) != 0 {
		t.Fatalf("clear retained below widget: %q", got)
	}
	if got := m.widgetContainer.Render(80); len(got) != 1 || got[0] != "" {
		t.Fatalf("clear did not restore above spacer: %q", got)
	}
}

// Pi's interactive-mode.ts mounts the footer after the editor in both modes.
func TestFullscreenShowsExtensionStatusBelowEditor(t *testing.T) {
	m := newFullscreenProbe(t)
	ui := &ExtUIContext{m: m}
	ui.SetStatus("probe", "extension-status-visible")
	rows := strings.Join(m.tuiInst.RenderSnapshot(80), "\n")
	editor, status := strings.Index(rows, widthx.CursorMarker), strings.Index(rows, "extension-status-visible")
	if editor < 0 || status <= editor {
		t.Fatalf("extension status missing below fullscreen editor: %q", rows)
	}
}
