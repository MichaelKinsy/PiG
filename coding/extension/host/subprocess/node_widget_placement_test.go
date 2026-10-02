package subprocess

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestNodeWidgetReplacementResetsPlacementAndOrder(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	entry := filepath.Join(t.TempDir(), "widget.mjs")
	write(t, entry, `export default function(pi) {
  pi.registerCommand("widget", {handler: (args, ctx) => {
    const {key, text, placement} = JSON.parse(args);
    ctx.ui.setWidget(key, () => ({render: () => [text]}), {placement});
  }});
}`)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			host := newTestHost(t)
			host.SetMode("tui")
			bridge := newTestBridge(&mockUIContext{})
			host.SetUIBridge(bridge)
			t.Cleanup(func() { host.Shutdown("test done") })
			exts, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "widget", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(exts) != 1 {
				t.Fatalf("LoadAll = %v, %v", exts, errs)
			}
			for _, step := range []struct{ args, key, text, placement string }{
				{`{"key":"first","text":"old","placement":"belowEditor"}`, "first", "old", "belowEditor"},
				{`{"key":"second","text":"second","placement":"belowEditor"}`, "second", "second", "belowEditor"},
				{`{"key":"first","text":"new"}`, "first", "new", "aboveEditor"},
			} {
				if err := exts[0].Commands["widget"].Handler(t.Context(), step.args); err != nil {
					t.Fatal(err)
				}
				pollUntil(t, 5*time.Second, "widget frame not published", func() bool {
					proxy := bridge.GetWidget("widget", step.key)
					return proxy != nil && slices.Equal(proxy.Lines(), []string{step.text})
				})
				placement, _ := bridge.GetWidget("widget", step.key).WidgetLayout()
				if (placement == "belowEditor") != (step.placement == "belowEditor") {
					t.Fatalf("%s placement = %q, want %q", step.key, placement, step.placement)
				}
			}
			_, firstOrder := bridge.GetWidget("widget", "first").WidgetLayout()
			_, secondOrder := bridge.GetWidget("widget", "second").WidgetLayout()
			if firstOrder <= secondOrder {
				t.Fatal("replacement did not move to the end")
			}
		})
	}
}
