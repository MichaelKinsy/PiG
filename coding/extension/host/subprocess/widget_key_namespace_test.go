package subprocess

import (
	"encoding/json"
	"slices"
	"testing"
)

// Pi keeps one widget map per side keyed by the widget key alone (interactive-mode.ts setExtensionWidget: removeExisting(key) from both
// maps, then set): a second extension that sets the same key replaces the first one's widget, and clearing the key clears it whichever
// extension set it.
func TestTwoExtensionsSettingOneWidgetKeyShareIt(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(newFakeUIContext())
	set := func(ext string, content []string) {
		t.Helper()
		args, err := json.Marshal(map[string]any{"key": "status", "content": content})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bridge.HandleCall(ext, &CallPayload{Method: "ui.setWidget", Args: args}); err != nil {
			t.Fatal(err)
		}
	}
	set("ext1", []string{"from one"})
	set("ext2", []string{"from two"})
	widgets := bridge.AllWidgets()
	if len(widgets) != 1 {
		t.Fatalf("%d widgets after two extensions set the key status, want one", len(widgets))
	}
	for _, proxy := range widgets {
		if got := proxy.Lines(); !slices.Equal(got, []string{"from two"}) {
			t.Fatalf("widget lines %q, want the second extension's", got)
		}
	}
	set("ext1", nil)
	if widgets := bridge.AllWidgets(); len(widgets) != 0 {
		t.Fatalf("%d widgets after the key was cleared, want none", len(widgets))
	}
}
