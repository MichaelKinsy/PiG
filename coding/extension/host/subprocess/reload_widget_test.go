//go:build !pig_strip_node_extensions

package subprocess

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestReloadDropsAWidgetTheReloadedExtensionDoesNotSetAgain: an extension sets a widget when its command runs. After a reload its factory
// has run again and nothing set the widget, so it is not drawn: Pi clears every extension widget when a /reload begins
// (interactive-mode.ts handleReloadCommand, resetExtensionUI, clearExtensionWidgets). The host kept the replaced generation's widget in
// the bridge, drawn for as long as the session ran.
func TestReloadDropsAWidgetTheReloadedExtensionDoesNotSetAgain(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	entry := filepath.Join(t.TempDir(), "widget.mjs")
	write(t, entry, `export default function(pi) {
  pi.registerCommand("widget", {handler: (_args, ctx) => ctx.ui.setWidget("status", ["set before the reload"])});
}`)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			h := newTestHost(t)
			h.SetMode("tui")
			bridge := NewUIBridge(func() {})
			h.SetUIBridge(bridge)
			t.Cleanup(func() { h.Shutdown("test done") })
			configs := []ExtConfig{{Name: "widget", Source: entry, Enabled: true, Isolation: isolation}}
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			exts, errs := h.LoadAll(t.Context(), configs)
			if len(errs) != 0 || len(exts) != 1 {
				t.Fatalf("LoadAll = %v, %v", exts, errs)
			}
			bridge.SetUIContext(&themedTestUI{UIContext: extension.NoopUIContext, theme: map[string]any{
				"foregrounds": map[string]string{},
				"backgrounds": map[string]string{},
			}})
			h.NotifyWidth(40)
			if err := exts[0].Commands["widget"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			pollUntil(t, testTimeout(t, 5*time.Second), "the widget is not set", func() bool { return bridge.GetWidget("widget", "status") != nil })
			if drawn := strings.Join(bridge.GetWidget("widget", "status").Render(40), "\n"); !strings.Contains(drawn, "set before the reload") {
				t.Fatalf("the widget draws %q, want its line", drawn)
			}

			reloaded, err := h.Reload(t.Context())
			if err != nil || len(reloaded) != 1 {
				t.Fatalf("Reload = %v, %v", reloaded, err)
			}
			// Reload returns when it has stopped the generation it replaced.
			if stale := bridge.GetWidget("widget", "status"); stale != nil {
				t.Fatalf("after the reload the bridge still draws the widget of the replaced generation: %q", stale.Render(40))
			}
			if widgets := bridge.AllWidgets(); len(widgets) != 0 {
				t.Fatalf("after the reload the bridge holds %d widgets, want none", len(widgets))
			}
		})
	}
}
