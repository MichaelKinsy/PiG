package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi keeps one widget map per side keyed by the widget key alone (interactive-mode.ts setExtensionWidget removes the key from both maps,
// then sets it), whichever extension calls it: a host-process extension and an extension process that set the same key replace each
// other's widget, and clearing the key clears it whoever set it.
func TestHostAndProcessExtensionsShareOneWidgetKeyNamespace(t *testing.T) {
	m := newFullscreenProbe(t)
	bridge := subprocess.NewUIBridge(func() {})
	m.opts.SubprocessUIBridge = bridge
	m.attachSubprocess()
	defer m.detachSubprocess()
	processSet := func(content any) {
		t.Helper()
		args, err := json.Marshal(map[string]any{"key": "status", "content": content})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bridge.HandleCall("proc", &subprocess.CallPayload{Method: "ui.setWidget", Args: args}); err != nil {
			t.Fatal(err)
		}
	}
	shown := func(want ...string) {
		t.Helper()
		rows := strings.Join(m.widgetContainer.Render(40), "\n") + strings.Join(m.widgetContainerBelow.Render(40), "\n")
		for _, text := range []string{"from host", "from process", "host again"} {
			if got, wanted := strings.Contains(rows, text), slices.Contains(want, text); got != wanted {
				t.Fatalf("widget %q shown = %t, want %t; rows %q", text, got, wanted, rows)
			}
		}
	}

	m.setExtensionWidget("status", tui.NewText("from host"), nil)
	shown("from host")
	processSet([]string{"from process"})
	shown("from process")
	m.setExtensionWidget("status", tui.NewText("host again"), nil)
	shown("host again")
	if widgets := bridge.AllWidgets(); len(widgets) != 0 {
		t.Fatalf("the extension process still holds %d widgets after the host-process extension replaced the key", len(widgets))
	}
	processSet(nil)
	shown()
}
