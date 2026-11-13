package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi clears every extension widget when a /reload begins: handleReloadCommand calls resetExtensionUI before it shows the reload box, and
// resetExtensionUI calls clearExtensionWidgets (interactive-mode.ts). The reloaded extensions set their widgets again; one an extension
// set before the reload and does not set again is gone. The widget of a host-process extension and the widget of an extension process
// both go.
func TestReloadClearsTheExtensionWidgets(t *testing.T) {
	m := newFullscreenProbe(t)
	bridge := subprocess.NewUIBridge(func() {})
	m.opts.SubprocessUIBridge = bridge
	m.attachSubprocess()
	defer m.detachSubprocess()
	if m.widgetContainerBelow == nil {
		m.widgetContainerBelow = tui.NewContainer()
	}
	rows := func() string {
		return strings.Join(m.widgetContainer.Render(40), "\n") + strings.Join(m.widgetContainerBelow.Render(40), "\n")
	}

	m.setExtensionWidget("host", tui.NewText("from the host process"), nil)
	args, err := json.Marshal(map[string]any{"key": "process", "content": []string{"from an extension process"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.HandleCall("proc", &subprocess.CallPayload{Method: "ui.setWidget", Args: args}); err != nil {
		t.Fatal(err)
	}
	if shown := rows(); !strings.Contains(shown, "from the host process") || !strings.Contains(shown, "from an extension process") {
		t.Fatalf("before the reload the widgets draw %q, want both", shown)
	}

	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if shown := rows(); strings.Contains(shown, "from the host process") || strings.Contains(shown, "from an extension process") {
		t.Errorf("after /reload the widgets set before it are still drawn: %q", shown)
	}
	if widgets := bridge.AllWidgets(); len(widgets) != 0 {
		t.Errorf("after /reload the bridge holds %d widgets of the extension processes, want none", len(widgets))
	}
}
