package subprocess

import (
	"encoding/json"
	"testing"
)

// The native SDKs' editor components send "ui.notify" as a notify frame when a
// component method fails (extensions/sdk/editor_component.go fail, its Python
// and Rust counterparts). The host must show it, as a ui.notify call is shown.
func TestHandleNotifyFromShowsUINotify(t *testing.T) {
	bridge := NewUIBridge(nil)
	type shown struct{ message, level string }
	got := make(chan shown, 1)
	bridge.SetNotifyFunc(func(message, level string) { got <- shown{message, level} })
	args, err := json.Marshal(map[string]any{"message": "editor render failed: boom", "level": "error"})
	if err != nil {
		t.Fatal(err)
	}
	bridge.HandleNotifyFrom("ext", nil, &NotifyPayload{Method: "ui.notify", Args: args})
	select {
	case value := <-got:
		if value != (shown{"editor render failed: boom", "error"}) {
			t.Fatalf("notify = %+v", value)
		}
	default:
		t.Fatal("a ui.notify notify frame was dropped")
	}
}
