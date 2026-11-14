package subprocess

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestWidgetGoesWithTheConnectionThatSetIt: a reload runs an extension again over a new connection, which registers before the generation
// it replaces is stopped. The widget that generation set goes when it is stopped, though the bridge already names the new connection: a
// widget the reloaded extension does not set again is not drawn after the reload, as in Pi, which clears its widgets when a reload begins.
// A key the new generation set meanwhile is its own widget and stays.
func TestWidgetGoesWithTheConnectionThatSetIt(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(&themedTestUI{UIContext: extension.NoopUIContext})
	var synced [][]string
	bridge.SetWidgetSyncFunc(func(widgets map[string]*PushProxy) {
		keys := make([]string, 0, len(widgets))
		for key := range widgets {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		synced = append(synced, keys)
	})
	// A widget of a view keeps its proxy and its view surface from frame to frame; the loader has one frame, so the host animates nothing.
	view := func(message string) json.RawMessage {
		return json.RawMessage(`{"root":{"kind":"loader","message":"` + message + `","indicator":{"frames":["*"]}}}`)
	}
	replaced, successor := &Conn{name: "ext"}, &Conn{name: "ext"}
	bridge.RegisterExtConn("ext", replaced, false)
	bridge.HandleWidgetPush("ext", replaced, &WidgetPushPayload{Key: "set once", Lines: []string{"from the replaced generation"}})
	bridge.HandleWidgetPush("ext", replaced, &WidgetPushPayload{Key: "set again", View: view("from the replaced generation")})
	before := bridge.GetWidget("ext", "set again")
	if before == nil || !strings.Contains(strings.Join(before.Render(60), "\n"), "from the replaced generation") {
		t.Fatalf("the widget of a view = %v, want it drawn", before)
	}

	bridge.RegisterExtConn("ext", successor, false)
	bridge.HandleWidgetPush("ext", successor, &WidgetPushPayload{Key: "set again", View: view("from the reloaded generation")})
	// The replaced generation runs until the reload stops it: what it sets after its successor registered is not drawn.
	bridge.HandleWidgetPush("ext", replaced, &WidgetPushPayload{Key: "set late", Lines: []string{"from the replaced generation"}})
	bridge.HandleWidgetPush("ext", replaced, &WidgetPushPayload{Key: "set again", View: view("from the replaced generation, late")})
	if late := bridge.GetWidget("ext", "set late"); late != nil {
		t.Errorf("a widget the replaced generation set after its successor registered is drawn: %q", late.Render(60))
	}
	bridge.ClearExtensionConn("ext", replaced)

	if stale := bridge.GetWidget("ext", "set once"); stale != nil {
		t.Errorf("the widget of the replaced generation is still drawn after it was stopped: %q", stale.Render(40))
	}
	kept := bridge.GetWidget("ext", "set again")
	if kept == nil || !strings.Contains(strings.Join(kept.Render(60), "\n"), "from the reloaded generation") {
		t.Fatalf("the widget the reloaded generation set = %v, want it drawn", kept)
	}
	if kept == before {
		t.Error("the reloaded generation's widget is the proxy of the replaced one, whose view surface belongs to the ended connection")
	}
	if last := synced[len(synced)-1]; !slices.Equal(last, []string{"set again"}) {
		t.Errorf("the widgets last laid out are %q, want the reloaded generation's alone", last)
	}
}
